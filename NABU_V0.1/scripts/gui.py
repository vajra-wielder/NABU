import os
import sys

sys.path.append(os.path.dirname(os.path.abspath(__file__)))  # Add scripts/ to Python path

# Add the Lib/site-packages directory to the Python path
base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
site_packages_dir = os.path.join(base_dir, 'python', 'Lib', 'site-packages')
sys.path.append(site_packages_dir)

from PyQt5.QtWidgets import QApplication, QMainWindow, QWidget, QVBoxLayout, QLineEdit, QPushButton, QTextEdit, QMessageBox
from PyQt5.QtCore import Qt
from search import search_articles
from parser import get_article, setup_database, parse_index

class WikipediaApp(QMainWindow):
    def __init__(self):
        super().__init__()

        # Set up the database and index
        self.conn = setup_database()
        self.index = parse_index()
        self.dump_file = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), 'data', 'enwiki-latest-pages-articles-multistream.xml.bz2')

        # Set up the main window
        self.setWindowTitle("Offline Wikipedia")
        self.setGeometry(100, 100, 800, 600)

        # Create the central widget
        central_widget = QWidget()
        self.setCentralWidget(central_widget)

        # Create the layout
        layout = QVBoxLayout()

        # Create the search bar
        self.search_entry = QLineEdit()
        self.search_entry.setPlaceholderText("Search Wikipedia...")
        layout.addWidget(self.search_entry)

        # Create the search button
        self.search_button = QPushButton("Search")
        self.search_button.clicked.connect(self.search)
        layout.addWidget(self.search_button)

        # Create the result display area
        self.result_text = QTextEdit()
        self.result_text.setReadOnly(True)
        layout.addWidget(self.result_text)

        # Set the layout
        central_widget.setLayout(layout)

    def search(self):
        query = self.search_entry.text()
        results = search_articles(query)

        self.result_text.clear()
        for title, content in results:
            self.result_text.append(f"Title: {title}\n")
            self.result_text.append(content)
            self.result_text.append("\n\n")

    def follow_hyperlink(self, link):
        # Fetch and display the linked article
        content = get_article(self.conn, link, self.index, self.dump_file)
        if content:
            self.result_text.clear()
            self.result_text.append(f"Title: {link}\n")
            self.result_text.append(content)
        else:
            QMessageBox.warning(self, "Article Not Found", f"Article not found: {link}")

if __name__ == '__main__':
    app = QApplication(sys.argv)
    window = WikipediaApp()
    window.show()
    sys.exit(app.exec_())