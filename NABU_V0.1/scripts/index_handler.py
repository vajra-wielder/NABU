import bz2
import xml.etree.ElementTree as ET
import os

def parse_index():
    BASE_DIR = os.path.dirname(os.path.abspath(__file__))
    DATA_DIR = os.path.join(BASE_DIR, '../data')
    index_file = os.path.join(DATA_DIR, 'enwiki-latest-pages-articles-multistream-index.xml.bz2')

    # Check if the index file exists
    if not os.path.exists(index_file):
        raise FileNotFoundError(f"Index file not found at: {index_file}")

    index = {}
    with bz2.open(index_file, 'rb') as f:
        for line in f:
            try:
                root = ET.fromstring(line)
                title = root.attrib['title']
                offset = int(root.attrib['id'])
                index[title] = offset
            except ET.ParseError:
                continue

    return index

def get_article_offset(title, index):
    return index.get(title, None)