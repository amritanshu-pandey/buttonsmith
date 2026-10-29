"""Native settings window: shows the local settings page in Qt WebEngine.

Run by `buttonsmith ui` as:
    python3 -I -c <this file> APP_ID APP_NAME ICON_PATH URL

APP_ID is the desktop file and icon name. Setting it as the desktop file
name makes it the Wayland app ID, so Plasma shows the app's own scalable
icon in the task switcher and taskbar.
"""

import sys

from PyQt6.QtCore import QTimer, QUrl
from PyQt6.QtGui import QGuiApplication, QIcon
from PyQt6.QtWebEngineWidgets import QWebEngineView
from PyQt6.QtWidgets import QApplication, QMessageBox

app_id, name, icon_path, url = sys.argv[1:5]

QGuiApplication.setDesktopFileName(app_id)
app = QApplication([name])
app.setApplicationName(name)
app.setApplicationDisplayName(name)
app.setWindowIcon(QIcon.fromTheme(app_id, QIcon(icon_path)))


class Window(QWebEngineView):
    def __init__(self):
        super().__init__()
        self.closing = False
        self.asking = False

    def closeEvent(self, event):
        if self.closing:
            event.accept()
            return
        event.ignore()
        if self.asking:
            return
        self.asking = True
        # Ask the page about unsaved changes. If it doesn't answer (still
        # loading, or broken), close anyway after a moment.
        timer = QTimer(self, singleShot=True, interval=1500)
        timer.timeout.connect(lambda: self.decide(False))
        timer.start()

        def answered(dirty):
            if timer.isActive():
                timer.stop()
                self.decide(bool(dirty))

        self.page().runJavaScript("typeof dirty === 'function' && dirty()", answered)

    def decide(self, dirty):
        self.asking = False
        if dirty:
            choice = QMessageBox.question(
                self,
                name,
                "You have unsaved changes. Close without saving?",
                QMessageBox.StandardButton.Discard | QMessageBox.StandardButton.Cancel,
                QMessageBox.StandardButton.Cancel,
            )
            if choice != QMessageBox.StandardButton.Discard:
                return
        self.closing = True
        self.close()


window = Window()
window.setWindowTitle(name)
window.resize(1000, 760)
window.load(QUrl(url))
window.show()
sys.exit(app.exec())
