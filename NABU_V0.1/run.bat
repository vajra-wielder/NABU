@echo off
REM Set the base directory (location of the batch file)
set BASE_DIR=%~dp0

REM Set the path to the portable Python interpreter on the pendrive
set PYTHON_PATH=%BASE_DIR%python\python.exe

REM Set the path to the scripts directory
set SCRIPTS_DIR=%BASE_DIR%scripts

REM Set the path to the parser script
set PARSER_SCRIPT=%SCRIPTS_DIR%\parser.py

REM Set the path to the GUI script
set GUI_SCRIPT=%SCRIPTS_DIR%\gui.py

REM Set the path to the database
set DATABASE_PATH=%BASE_DIR%data\wikipedia.db

REM Check if the portable Python interpreter is available
if not exist "%PYTHON_PATH%" (
    echo Error: Portable Python interpreter not found at %PYTHON_PATH%.
    pause
    exit /b 1
)

REM Check if the user wants to force a rebuild
if "%1" == "--rebuild" (
    echo Forcing database rebuild...
    del "%DATABASE_PATH%" 2>nul
)

REM Check if the database exists
if not exist "%DATABASE_PATH%" (
    echo Database not found. Running parser to populate the database...
    "%PYTHON_PATH%" "%PARSER_SCRIPT%"
    if errorlevel 1 (
        echo Error: Failed to populate the database.
        pause
        exit /b 1
    )
) else (
    echo Database found. Skipping parser.
)

REM Run the app using the portable Python interpreter
echo Starting Offline Wikipedia App...
"%PYTHON_PATH%" "%GUI_SCRIPT%"

REM Pause to keep the terminal open (optional)
pause