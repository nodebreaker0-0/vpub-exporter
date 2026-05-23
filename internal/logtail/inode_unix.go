//go:build linux || darwin
// +build linux darwin

package logtail

import (
	"os"
	"syscall"
)

func init() {
	inodeOf = func(fi os.FileInfo) inodeKey {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			return inodeKey{dev: uint64(st.Dev), ino: uint64(st.Ino)}
		}
		return inodeOfStub(fi)
	}
}
