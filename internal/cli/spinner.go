package cli

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/mattn/go-isatty"
)

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinner renders a simple animated indicator on stderr.
// It only activates when stderr is a TTY and JSON output is off.
type spinner struct {
	mu      sync.Mutex
	msg     string
	stop    chan struct{}
	stopped chan struct{}
	active  bool
}

func newSpinner(msg string) *spinner {
	return &spinner{msg: msg, stop: make(chan struct{}), stopped: make(chan struct{})}
}

// start begins the animation. No-op if not a TTY or --json is set.
func (sp *spinner) start() {
	if jsonOutput || !isatty.IsTerminal(os.Stderr.Fd()) {
		close(sp.stopped)
		return
	}
	sp.active = true
	go sp.run()
}

func (sp *spinner) run() {
	defer close(sp.stopped)
	i := 0
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-sp.stop:
			sp.clear()
			return
		case <-ticker.C:
			sp.mu.Lock()
			msg := sp.msg
			sp.mu.Unlock()
			fmt.Fprintf(os.Stderr, "\r\033[K%s %s", spinFrames[i%len(spinFrames)], msg)
			i++
		}
	}
}

// update changes the displayed message mid-flight.
func (sp *spinner) update(msg string) {
	sp.mu.Lock()
	sp.msg = msg
	sp.mu.Unlock()
}

// done stops the spinner and clears the line.
func (sp *spinner) done() {
	if !sp.active {
		return
	}
	close(sp.stop)
	<-sp.stopped
}

func (sp *spinner) clear() {
	fmt.Fprintf(os.Stderr, "\r\033[K")
}
