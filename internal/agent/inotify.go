package agent

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// watcher reports file events as full paths. It watches directories only;
// an event names the directory entry that changed.
type watcher struct {
	fd     int // raw fd for InotifyAddWatch; f.Fd() would make f blocking
	f      *os.File
	events chan string

	mu   sync.Mutex
	dirs map[string]int // dir → watch descriptor
	wds  map[int]string
}

const watchMask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MODIFY | unix.IN_ATTRIB |
	unix.IN_CLOSE_WRITE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO |
	unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_ONLYDIR

func newWatcher() (*watcher, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, err
	}
	w := &watcher{
		fd: fd,
		// A non-blocking fd in os.File uses the runtime poller, so Close
		// ends a blocked Read.
		f:      os.NewFile(uintptr(fd), "inotify"),
		events: make(chan string, 256),
		dirs:   map[string]int{},
		wds:    map[int]string{},
	}
	go w.read()
	return w, nil
}

// add watches dir, or its nearest existing ancestor when dir is missing.
func (w *watcher) add(dir string) error {
	for {
		w.mu.Lock()
		_, ok := w.dirs[dir]
		w.mu.Unlock()
		if ok {
			return nil
		}
		wd, err := unix.InotifyAddWatch(w.fd, dir, watchMask)
		if err == nil {
			w.mu.Lock()
			w.dirs[dir], w.wds[wd] = wd, dir
			w.mu.Unlock()
			return nil
		}
		if !errors.Is(err, unix.ENOENT) && !errors.Is(err, unix.ENOTDIR) {
			return err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return err
		}
		dir = parent
	}
}

func (w *watcher) read() {
	defer close(w.events)
	buf := make([]byte, 64*1024)
	for {
		n, err := w.f.Read(buf)
		if err != nil {
			return
		}
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off]))
			name := buf[off+unix.SizeofInotifyEvent : off+unix.SizeofInotifyEvent+int(ev.Len)]
			off += unix.SizeofInotifyEvent + int(ev.Len)
			w.mu.Lock()
			dir := w.wds[int(ev.Wd)]
			if ev.Mask&unix.IN_IGNORED != 0 {
				delete(w.wds, int(ev.Wd))
				delete(w.dirs, dir)
			}
			w.mu.Unlock()
			if dir == "" {
				continue
			}
			p := dir
			if i := indexZero(name); i > 0 {
				p = filepath.Join(dir, string(name[:i]))
			}
			select {
			case w.events <- p:
			default: // the drift loop is busy; the full check covers a lost event
			}
		}
	}
}

func indexZero(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return len(b)
}

func (w *watcher) close() { _ = w.f.Close() }
