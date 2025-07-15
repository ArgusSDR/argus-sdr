package logger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type Logger struct {
	*log.Logger
}

func New() *Logger {
	logger := log.New(os.Stdout, "", 0)
	logger.SetOutput(&timestampWriter{})
	return &Logger{
		Logger: logger,
	}
}

type timestampWriter struct{}

func (w *timestampWriter) Write(p []byte) (n int, err error) {
	// Get caller info for file:line
	_, file, line, ok := runtime.Caller(4) // Adjust call stack depth
	var fileInfo string
	if ok {
		fileInfo = fmt.Sprintf(" %s:%d:", filepath.Base(file), line)
	}
	
	// Format timestamp with milliseconds
	timestamp := time.Now().Format("2006/01/02 15:04:05.000")
	
	// Write formatted log entry
	formatted := fmt.Sprintf("%s%s %s", timestamp, fileInfo, string(p))
	return os.Stdout.Write([]byte(formatted))
}

func (l *Logger) Info(format string, v ...interface{}) {
	l.Logger.Printf("[INFO] "+format, v...)
}

func (l *Logger) Error(format string, v ...interface{}) {
	l.Logger.Printf("[ERROR] "+format, v...)
}

func (l *Logger) Debug(format string, v ...interface{}) {
	l.Logger.Printf("[DEBUG] "+format, v...)
}

func (l *Logger) Warn(format string, v ...interface{}) {
	l.Logger.Printf("[WARN] "+format, v...)
}

func (l *Logger) Fatal(format string, v ...interface{}) {
	l.Logger.Printf("[FATAL] "+format, v...)
	os.Exit(1)
}