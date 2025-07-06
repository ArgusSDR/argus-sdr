package logger

import (
	"log"
	"os"
)

type Logger struct {
	*log.Logger
}

func New() *Logger {
	return &Logger{
		Logger: log.New(os.Stdout, "", log.LstdFlags|log.Lshortfile),
	}
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