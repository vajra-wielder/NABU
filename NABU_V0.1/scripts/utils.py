import os
import sys

# Add the Lib/site-packages directory to the Python path
base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
site_packages_dir = os.path.join(base_dir, 'python', 'Lib', 'site-packages')
sys.path.append(site_packages_dir)

import re
import mwparserfromhell

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