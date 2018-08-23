package watch

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/windmilleng/fsevents"
)

type darwinNotify struct {
	stream *fsevents.EventStream
	events chan FileEvent
	errors chan error
	stop   chan struct{}

	// TODO(nick): This mutex is needed for the case where we add paths after we
	// start watching. But because fsevents supports recursive watches, we don't
	// actually need this feature. We should change the api contract of wmNotify
	// so that, for recursive watches, we can guarantee that the path list doesn't
	// change.
	sm *sync.Mutex
}

func (d *darwinNotify) isTrackingPath(path string) bool {
	d.sm.Lock()
	defer d.sm.Unlock()
	for _, p := range d.stream.Paths {
		if p == path {
			return true
		}
	}
	return false
}

func (d *darwinNotify) loop() {
	ignoredSpuriousEvents := make(map[string]bool, 0)
	for {
		select {
		case <-d.stop:
			return
		case events, ok := <-d.stream.Events:
			if !ok {
				return
			}

			for _, e := range events {
				e.Path = filepath.Join("/", e.Path)

				// ignore the first event that says the watched directory
				// has been created. these are fired spuriously on initiation.
				if e.Flags&fsevents.ItemCreated == fsevents.ItemCreated {
					if !ignoredSpuriousEvents[e.Path] && d.isTrackingPath(e.Path) {
						ignoredSpuriousEvents[e.Path] = true
						continue
					}
				}

				d.events <- FileEvent{
					Path: e.Path,
				}
			}
		}
	}
}

func (d *darwinNotify) Add(name string) error {
	d.sm.Lock()
	defer d.sm.Unlock()

	es := d.stream

	// Check if this is a subdirectory of any of the paths
	// we're already watching.
	for _, parent := range es.Paths {
		isChild := pathIsChildOf(name, parent)
		if isChild {
			return nil
		}
	}

	es.Paths = append(es.Paths, name)
	if len(es.Paths) == 1 {
		go d.loop()
		es.Start()
	} else {
		es.Restart()
	}

	return nil
}

func (d *darwinNotify) Close() error {
	d.sm.Lock()
	defer d.sm.Unlock()

	d.stream.Stop()
	close(d.errors)
	close(d.stop)

	return nil
}

func (d *darwinNotify) Events() chan FileEvent {
	return d.events
}

func (d *darwinNotify) Errors() chan error {
	return d.errors
}

func NewWatcher() (Notify, error) {
	dw := &darwinNotify{
		stream: &fsevents.EventStream{
			Latency: 1 * time.Millisecond,
			Flags:   fsevents.FileEvents,
		},
		sm:     &sync.Mutex{},
		events: make(chan FileEvent),
		errors: make(chan error),
		stop:   make(chan struct{}),
	}

	return dw, nil
}

var _ Notify = &darwinNotify{}