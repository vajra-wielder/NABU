import bz2
import xml.etree.ElementTree as ET
import sqlite3
import re
import os
import sys

sys.path.append(os.path.dirname(os.path.abspath(__file__)))  # Add scripts/ to Python path
from index_handler import parse_index

# Add the Lib/site-packages directory to the Python path
base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
site_packages_dir = os.path.join(base_dir, 'python', 'Lib', 'site-packages')
sys.path.append(site_packages_dir)
import mwparserfromhell


def setup_database():
    BASE_DIR = os.path.dirname(os.path.abspath(__file__))
    DATA_DIR = os.path.join(BASE_DIR, '../data')
    db_path = os.path.join(DATA_DIR, 'wikipedia.db')

    # Check if the database already exists
    if os.path.exists(db_path):
        print("Database already exists. Skipping parsing.")
        return None

    conn = sqlite3.connect(db_path)
    cursor = conn.cursor()

    # Create tables
    cursor.execute('''
        CREATE TABLE IF NOT EXISTS articles (
            id INTEGER PRIMARY KEY,
            title TEXT,
            content TEXT
        )
    ''')
    cursor.execute('''
        CREATE TABLE IF NOT EXISTS links (
            id INTEGER PRIMARY KEY,
            source_article_id INTEGER,
            target_article_title TEXT,
            FOREIGN KEY (source_article_id) REFERENCES articles(id)
        )
    ''')
    cursor.execute('''
        CREATE TABLE IF NOT EXISTS glossary (
            id INTEGER PRIMARY KEY,
            article_id INTEGER,
            term TEXT,
            definition TEXT,
            FOREIGN KEY (article_id) REFERENCES articles(id)
        )
    ''')
    cursor.execute('''
        CREATE TABLE IF NOT EXISTS geodata (
            id INTEGER PRIMARY KEY,
            article_id INTEGER,
            latitude REAL,
            longitude REAL,
            FOREIGN KEY (article_id) REFERENCES articles(id)
        )
    ''')
    cursor.execute('''
        CREATE TABLE IF NOT EXISTS tables (
            id INTEGER PRIMARY KEY,
            article_id INTEGER,
            table_data TEXT,
            FOREIGN KEY (article_id) REFERENCES articles(id)
        )
    ''')
    cursor.execute('''
        CREATE TABLE IF NOT EXISTS media (
            id INTEGER PRIMARY KEY,
            article_id INTEGER,
            media_type TEXT,
            media_url TEXT,
            FOREIGN KEY (article_id) REFERENCES articles(id)
        )
    ''')
    cursor.execute('''
        CREATE TABLE IF NOT EXISTS infoboxes (
            id INTEGER PRIMARY KEY,
            article_id INTEGER,
            infobox_data TEXT,
            FOREIGN KEY (article_id) REFERENCES articles(id)
        )
    ''')
    cursor.execute('''
        CREATE TABLE IF NOT EXISTS metadata (
            id INTEGER PRIMARY KEY,
            article_id INTEGER,
            metadata_type TEXT,
            metadata_value TEXT,
            FOREIGN KEY (article_id) REFERENCES articles(id)
        )
    ''')
    conn.commit()
    return conn

def parse_dump(conn):
    BASE_DIR = os.path.dirname(os.path.abspath(__file__))
    DATA_DIR = os.path.join(BASE_DIR, '../data')
    dump_file = os.path.join(DATA_DIR, 'enwiki-latest-pages-articles-multistream.xml.bz2')

    # Parse the index
    index = parse_index()

    with bz2.open(dump_file, 'rb') as f:
        for title, offset in index.items():
            f.seek(offset)
            line = f.readline()
            try:
                root = ET.fromstring(line)
                title = root.find('title').text
                text = root.find('text').text

                # Insert article into database
                cursor = conn.cursor()
                cursor.execute('INSERT INTO articles (title, content) VALUES (?, ?)', (title, text))
                article_id = cursor.lastrowid

                # Extract and insert hyperlinks
                links = extract_links(text)
                for link in links:
                    cursor.execute('INSERT INTO links (source_article_id, target_article_title) VALUES (?, ?)', (article_id, link))

                # Extract and insert glossary terms
                glossary_entries = extract_glossary(text)
                for term, definition in glossary_entries:
                    cursor.execute('INSERT INTO glossary (article_id, term, definition) VALUES (?, ?, ?)', (article_id, term, definition))

                # Extract and insert geographical data
                coordinates = extract_coordinates(text)
                for latitude, longitude in coordinates:
                    cursor.execute('INSERT INTO geodata (article_id, latitude, longitude) VALUES (?, ?, ?)', (article_id, latitude, longitude))

                # Extract and insert tables
                tables = extract_tables(text)
                for table_data in tables:
                    cursor.execute('INSERT INTO tables (article_id, table_data) VALUES (?, ?)', (article_id, table_data))

                # Extract and insert media
                media_links = extract_media(text)
                for media_type, media_url in media_links:
                    cursor.execute('INSERT INTO media (article_id, media_type, media_url) VALUES (?, ?, ?)', (article_id, media_type, media_url))

                # Extract and insert infoboxes
                infoboxes = extract_infoboxes(text)
                for infobox_data in infoboxes:
                    cursor.execute('INSERT INTO infoboxes (article_id, infobox_data) VALUES (?, ?)', (article_id, infobox_data))

                # Extract and insert metadata
                categories, templates = extract_metadata(text)
                for category in categories:
                    cursor.execute('INSERT INTO metadata (article_id, metadata_type, metadata_value) VALUES (?, ?, ?)', (article_id, 'category', category))
                for template in templates:
                    cursor.execute('INSERT INTO metadata (article_id, metadata_type, metadata_value) VALUES (?, ?, ?)', (article_id, 'template', template))

            except ET.ParseError:
                continue

    conn.commit()

def extract_links(text):
    return re.findall(r'\[\[(.*?)\]\]', text)

def extract_glossary(text):
    return re.findall(r'\{\{glossary\|(.*?)\|(.*?)\}\}', text)

def extract_coordinates(text):
    return re.findall(r'\{\{coord\|(.*?)\|(.*?)\}\}', text)

def extract_tables(text):
    wikicode = mwparserfromhell.parse(text)
    tables = wikicode.filter_tags(matches=lambda node: node.tag == 'table')
    return [str(table) for table in tables]

def extract_media(text):
    return re.findall(r'\[\[(File|Image):(.*?)\]\]', text)

def extract_infoboxes(text):
    wikicode = mwparserfromhell.parse(text)
    infoboxes = wikicode.filter_templates(matches=lambda node: node.name.startswith('Infobox'))
    return [str(infobox) for infobox in infoboxes]

def extract_metadata(text):
    categories = re.findall(r'\[\[Category:(.*?)\]\]', text)
    templates = re.findall(r'\{\{(.*?)\}\}', text)
    return categories, templates

if __name__ == '__main__':
    conn = setup_database()
    if conn:
        parse_dump(conn)
        conn.close()