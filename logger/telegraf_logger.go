package logger

import "fmt"

type TelegrafLogger struct {
	description string
}

func NewTelegrafLog(description string) TelegrafLogger {
	return TelegrafLogger{description}
}

// Errorf logs an error message, patterned after log.Printf.
func (t TelegrafLogger) Errorf(format string, args ...interface{}) {
	V(0).Printf(t.addDescriptionToFormat(format), args...)
}

// Error logs an error message, patterned after log.Print.
func (t TelegrafLogger) Error(args ...interface{}) {
	V(0).Println(t.addDescriptionToArgs(args...))
}

// Debugf logs a debug message, patterned after log.Printf.
func (t TelegrafLogger) Debugf(format string, args ...interface{}) {
	V(3).Printf(t.addDescriptionToFormat(format), args...)
}

// Debug logs a debug message, patterned after log.Print.
func (t TelegrafLogger) Debug(args ...interface{}) {
	V(3).Println(t.addDescriptionToArgs(args...))
}

// Warnf logs a warning message, patterned after log.Printf.
func (t TelegrafLogger) Warnf(format string, args ...interface{}) {
	V(1).Printf(t.addDescriptionToFormat(format), args...)
}

// Warn logs a warning message, patterned after log.Print.
func (t TelegrafLogger) Warn(args ...interface{}) {
	V(1).Println(t.addDescriptionToArgs(args...))
}

// Infof logs an information message, patterned after log.Printf.
func (t TelegrafLogger) Infof(format string, args ...interface{}) {
	V(2).Printf(t.addDescriptionToFormat(format), args...)
}

// Info logs an information message, patterned after log.Print.
func (t TelegrafLogger) Info(args ...interface{}) {
	V(2).Println(t.addDescriptionToArgs(args...))
}

// addDescriptionToArgs adds the input description to the log.
func (t TelegrafLogger) addDescriptionToArgs(args ...interface{}) interface{} {
	return append([]interface{}{fmt.Sprintf("%s: ", t.description)}, args)
}

// addDescriptionToArgs adds the input description to the log.
func (t TelegrafLogger) addDescriptionToFormat(format string) string {
	return fmt.Sprintf("%s: %s", t.description, format)
}
