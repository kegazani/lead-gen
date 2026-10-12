//go:build linux

package main

import (
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

func unixPeerCredentials(connection *net.UnixConn) (pid, uid, gid int, err error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return 0, 0, 0, err
	}
	var credential *unix.Ucred
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		credential, controlErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, 0, 0, err
	}
	if controlErr != nil || credential == nil {
		return 0, 0, 0, errors.New("Unix peer credentials unavailable")
	}
	return int(credential.Pid), int(credential.Uid), int(credential.Gid), nil
}
