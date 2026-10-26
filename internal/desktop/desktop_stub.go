//go:build !linux || js

package desktop

import "errors"

// ErrCanceled is returned when the initial workspace chooser is dismissed.
var ErrCanceled = errors.New("workspace selection canceled")

// Options configures the native window.
type Options struct {
	Title       string
	Width       int
	Height      int
	InitialRoot string
	Start       func(root string) (url string, err error)
	Switch      func(root string) (url string, err error)
}

// Available reports whether the native desktop host can run.
func Available() bool { return false }

// Run is unavailable outside Linux/GTK.
func Run(Options) error {
	return errors.New("native desktop UI requires Linux with GTK3 and WebKitGTK 4.1; use --ui browser or --ui headless")
}
