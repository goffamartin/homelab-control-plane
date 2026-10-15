//go:build !linux

package managed

import "os"

func openPrivateStore(string) (*os.Root, *os.File, error)   { return nil, nil, ErrStore }
func openStoreFile(*os.Root, string, int) (*os.File, error) { return nil, ErrStore }
