package check

import (
	"context"
	"log"
	"net"
	"sync"
	"time"
)

const (
	statusOk       = 0
	statusCritical = 2
	statusUnknown  = 3
)

type checkError struct {
	status      int
	description string
}

// baseCheck perform a check (using doCheck function) and maintain TCP connection open to detect service failure quickly
type baseCheck struct {
	metricName   string
	item         string
	tcpAddresses []string
	doCheck      func(ctx context.Context) checkError
	//sendData     []byte
	//expectedData []byte

	timer    *time.Timer
	dialer   *net.Dialer
	triggerC chan interface{}
	cancel   func()
	wg       sync.WaitGroup
}

// NewTCP ...
func newBase(tcpAddresses []string, metricName string, item string, doCheck func(context.Context) checkError) *baseCheck {
	return &baseCheck{
		metricName:   metricName,
		item:         item,
		tcpAddresses: tcpAddresses,
		doCheck:      doCheck,

		dialer:   &net.Dialer{},
		timer:    time.NewTimer(0),
		triggerC: make(chan interface{}),
	}
}

// Run execute the TCP check
func (bc *baseCheck) Run(ctx context.Context) {
	// Open connectionS to address
	// when openned, keep checking that port stay open
	// when port goes from open to close, back to step 1
	// If step 1 fail => trigger check
	// trigger check every minutes (or 30 seconds)
	result := checkError{
		status:      statusOk,
		description: "initial status - description is ignored",
	}
	for {
		select {
		case <-ctx.Done():
			if bc.cancel != nil {
				bc.cancel()
				bc.cancel = nil
			}
			bc.wg.Wait()
			return
		case <-bc.triggerC:
			if !bc.timer.Stop() {
				<-bc.timer.C
			}
			result = bc.check(ctx, result)
		case <-bc.timer.C:
			result = bc.check(ctx, result)
		}
	}
}

func (bc *baseCheck) check(ctx context.Context, previousStatus checkError) checkError {
	// do the check
	// if successful, ensure socket are open
	// if fail, ensure socket are closed
	// if just fail (ok -> critical), do a fast check
	result := bc.doCheck(ctx)
	if ctx.Err() != nil {
		return previousStatus
	}
	timerDone := false
	if result.status != statusOk {
		if bc.cancel != nil {
			bc.cancel()
			bc.wg.Wait()
			bc.cancel = nil
		}
		if previousStatus.status == statusOk {
			bc.timer.Reset(30 * time.Second)
			timerDone = true
		}
	} else {
		bc.openSockets(ctx)
	}

	if !timerDone {
		bc.timer.Reset(time.Minute)
	}
	log.Printf("DBG2: check for %#v on %#v: %v", bc.metricName, bc.item, result)
	return result
}

func (bc *baseCheck) openSockets(ctx context.Context) {
	if bc.cancel != nil {
		// socket are already open
		return
	}
	ctx2, cancel := context.WithCancel(ctx)
	bc.cancel = cancel

	for _, addr := range bc.tcpAddresses {
		addr := addr
		bc.wg.Add(1)
		go func() {
			defer bc.wg.Done()
			bc.openSocket(ctx2, addr)
		}()
	}
}

func (bc *baseCheck) openSocket(ctx context.Context, addr string) {
	for ctx.Err() == nil {
		bc.openSocketOnce(ctx, addr)
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
		}
	}
}

func (bc *baseCheck) openSocketOnce(ctx context.Context, addr string) {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := bc.dialer.DialContext(ctx2, "tcp", addr)
	if err != nil {
		log.Printf("DBG2: fail to open TCP connection to %#v: %v", addr, err)
		bc.triggerC <- nil
		return
	}
	defer conn.Close()
	buffer := make([]byte, 4096)
	for ctx.Err() == nil {
		err := conn.SetDeadline(time.Now().Add(time.Second))
		if err != nil {
			log.Printf("DBG2: Unable to SetDeadline() for %#v: %v", addr, err)
			bc.triggerC <- nil
			return
		}
		_, err = conn.Read(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			log.Printf("DBG2: Unable to Read() from %#v: %v", addr, err)
			bc.triggerC <- nil
			return
		}
	}
}
