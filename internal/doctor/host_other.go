//go:build !linux

package doctor

import "os"

// Do not claim POSIX ownership and mode validation on unsupported platforms.
func safeOwner(os.FileInfo) bool                 { return false }
func safeSticky(os.FileInfo) bool                { return false }
func singleLink(os.FileInfo) bool                { return false }
func openReadOnly(path string) (*os.File, error) { return nil, errUnsafeFile }
