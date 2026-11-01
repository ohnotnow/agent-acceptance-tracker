package aat

import "fmt"

// CLIError is written to stderr as {"error": {"code": ..., "message": ...}}.
type CLIError struct {
	Code     string
	Message  string
	ExitCode int
}

func (e *CLIError) Error() string { return e.Message }

func usageErr(format string, a ...any) *CLIError {
	return &CLIError{"usage", fmt.Sprintf(format, a...), 64}
}

func validationErr(format string, a ...any) *CLIError {
	return &CLIError{"validation", fmt.Sprintf(format, a...), 65}
}

func notFoundErr(format string, a ...any) *CLIError {
	return &CLIError{"not_found", fmt.Sprintf(format, a...), 66}
}

func conflictErr(format string, a ...any) *CLIError {
	return &CLIError{"conflict", fmt.Sprintf(format, a...), 67}
}

func uninitialisedErr(path string) *CLIError {
	return &CLIError{"uninitialised", fmt.Sprintf("no aat database at %s - run 'aat init' first", path), 1}
}
