//go:build linux

package hooks

import (
	"syscall"
)

func initPlatformWatcher(dir string) (inboxWaiter, error) {
	inFd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC)
	if err != nil {
		return nil, err
	}
	if _, err := syscall.InotifyAddWatch(inFd, dir, syscall.IN_CREATE|syscall.IN_MOVED_TO); err != nil {
		_ = syscall.Close(inFd)
		return nil, err
	}
	epFd, err := syscall.EpollCreate1(syscall.EPOLL_CLOEXEC)
	if err != nil {
		_ = syscall.Close(inFd)
		return nil, err
	}
	ev := syscall.EpollEvent{Events: syscall.EPOLLIN, Fd: int32(inFd)}
	if err := syscall.EpollCtl(epFd, syscall.EPOLL_CTL_ADD, inFd, &ev); err != nil {
		_ = syscall.Close(inFd)
		_ = syscall.Close(epFd)
		return nil, err
	}
	return &inotifyWaiter{inFd: inFd, epFd: epFd}, nil
}

type inotifyWaiter struct {
	inFd int
	epFd int
}

func (w *inotifyWaiter) Wait(timeoutMs int) (bool, error) {
	events := make([]syscall.EpollEvent, 1)
	for {
		n, err := syscall.EpollWait(w.epFd, events, timeoutMs)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return false, err
		}
		if n == 0 {
			return false, nil
		}
		buf := make([]byte, 4096)
		_, _ = syscall.Read(w.inFd, buf)
		return true, nil
	}
}

func (w *inotifyWaiter) Close() error {
	_ = syscall.Close(w.epFd)
	return syscall.Close(w.inFd)
}
