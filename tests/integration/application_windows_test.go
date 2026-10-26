//go:build integration && windows

package integration

// Windows does not support os.Process.Signal(os.Interrupt). The fixture's
// enclosing native Job owns the application and all nested worker Jobs; Stop
// terminates and reaps that tree. Graceful service shutdown is covered separately.
func signalApplicationStop(pid int, binary, config string) bool { return false }
func reapApplicationOrphans(markers ...string) ([]int, error)   { return nil, nil }
