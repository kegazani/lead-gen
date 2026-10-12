//go:build !linux

package main

import (
	"errors"
	"net"
)

func unixPeerCredentials(_ *net.UnixConn) (pid, uid, gid int, err error) {
	return 0, 0, 0, errors.New("release ledger IPC requires Linux Unix peer credentials")
}
