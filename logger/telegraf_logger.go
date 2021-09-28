package logger

type TelegrafLogger struct{}

func NewTelegrafLog() TelegrafLogger {
	return TelegrafLogger{}
}

// Errorf logs an error message, patterned after log.Printf.
func (t TelegrafLogger) Errorf(format string, args ...interface{}) {
	V(0).Printf(format, args...)
}

// Error logs an error message, patterned after log.Print.
func (t TelegrafLogger) Error(args ...interface{}) {
	V(0).Println(args...)
}

// Debugf logs a debug message, patterned after log.Printf.
func (t TelegrafLogger) Debugf(format string, args ...interface{}) {
	V(3).Printf(format, args...)
}

// Debug logs a debug message, patterned after log.Print.
func (t TelegrafLogger) Debug(args ...interface{}) {
	V(3).Println(args...)
}

// Warnf logs a warning message, patterned after log.Printf.
func (t TelegrafLogger) Warnf(format string, args ...interface{}) {
	V(1).Printf(format, args...)
}

// Warn logs a warning message, patterned after log.Print.
func (t TelegrafLogger) Warn(args ...interface{}) {
	V(1).Println(args...)
}

// Infof logs an information message, patterned after log.Printf.
func (t TelegrafLogger) Infof(format string, args ...interface{}) {
	V(2).Printf(format, args...)
}

// Info logs an information message, patterned after log.Print.
func (t TelegrafLogger) Info(args ...interface{}) {
	V(2).Println(args...)
}
