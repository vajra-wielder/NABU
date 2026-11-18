import sqlite3
import os

# Add the Lib/site-packages directory to the Python path
base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
site_packages_dir = os.path.join(base_dir, 'python', 'Lib', 'site-packages')
sys.path.append(site_packages_dir)

BASE_DIR = os.path.dirname(os.path.abspath(__file__))
DATA_DIR = os.path.join(BASE_DIR, '../data')

def search_articles(query):
    db_path = os.path.join(DATA_DIR, 'wikipedia.db')
    conn = sqlite3.connect(db_path)
    cursor = conn.cursor()

    cursor.execute('''
        CREATE VIRTUAL TABLE IF NOT EXISTS articles_fts USING fts5(title, content);
    ''')
    cursor.execute('''
        INSERT INTO articles_fts SELECT title, content FROM articles;
    ''')

    cursor.execute('''
        SELECT title, content FROM articles_fts WHERE articles_fts MATCH ?;
    ''', (query,))

    results = cursor.fetchall()
    conn.close()
    return results