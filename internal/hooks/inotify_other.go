//go:build !linux

package hooks

import "errors"

func initPlatformWatcher(dir string) (inboxWaiter, error) {
	return nil, errors.New("inotify only supported on linux")
}
