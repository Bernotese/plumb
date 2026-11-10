package agent

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// watcher reports file events as full paths. It watches directories only;
// an event names the directory entry that changed. When events are lost
// (kernel queue overflow, or the drift loop is too slow) it signals
// overflow, and the drift loop runs a full check.
type watcher struct {
	fd       int // raw fd for the watch calls; f.Fd() would make f blocking
	f        *os.File
	events   chan string
	overflow chan struct{}

	mu      sync.Mutex
	dirs    map[string]int // watched dir → watch descriptor
	wds     map[int]string
	noSpace bool // ENOSPC was logged
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
		f:        os.NewFile(uintptr(fd), "inotify"),
		events:   make(chan string, 256),
		overflow: make(chan struct{}, 1),
		dirs:     map[string]int{},
		wds:      map[int]string{},
	}
	go w.read()
	return w, nil
}

// sync makes the watched set equal to dirs. A missing dir is replaced by
// its nearest existing ancestor, so its creation is seen; watches that are
// no longer needed are removed.
func (w *watcher) sync(dirs map[string]bool, log *slog.Logger) {
	want := map[string]bool{}
	for d := range dirs {
		if real, ok := existingAncestor(d); ok {
			want[real] = true
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for d, wd := range w.dirs {
		if !want[d] {
			_, _ = unix.InotifyRmWatch(w.fd, uint32(wd))
			delete(w.dirs, d)
			delete(w.wds, wd)
		}
	}
	for d := range want {
		if _, ok := w.dirs[d]; ok {
			continue
		}
		wd, err := unix.InotifyAddWatch(w.fd, d, watchMask)
		if err != nil {
			if errors.Is(err, unix.ENOSPC) && !w.noSpace {
				w.noSpace = true
				log.Error("inotify watch limit reached (fs.inotify.max_user_watches); drift on some paths is found only by the full check")
			} else if !errors.Is(err, unix.ENOSPC) {
				log.Debug("watch", "dir", d, "err", err)
			}
			continue
		}
		w.dirs[d], w.wds[wd] = wd, d
	}
}

// existingAncestor returns dir or its nearest existing ancestor directory.
func existingAncestor(dir string) (string, bool) {
	for {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func (w *watcher) signalOverflow() {
	select {
	case w.overflow <- struct{}{}:
	default:
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
			if ev.Mask&unix.IN_Q_OVERFLOW != 0 {
				w.signalOverflow()
				continue
			}
			w.mu.Lock()
			dir := w.wds[int(ev.Wd)]
			if ev.Mask&unix.IN_IGNORED != 0 {
				delete(w.wds, int(ev.Wd))
				if w.dirs[dir] == int(ev.Wd) {
					delete(w.dirs, dir)
				}
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
			default:
				w.signalOverflow() // the drift loop is behind: check everything
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

// watched returns the number of watched directories (for tests).
func (w *watcher) watched() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.dirs)
}

func (w *watcher) close() { _ = w.f.Close() }
