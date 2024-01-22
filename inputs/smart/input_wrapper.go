package smart

import (
	"strings"
	"sync"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs/smart"
)

type inputWrapper struct {
	*smart.Smart
	devices []string
	l       sync.Mutex
}

func (iw *inputWrapper) Gather(acc telegraf.Accumulator) error {
	iw.l.Lock()
	defer iw.l.Unlock()

	out, err := runCmd(iw.Smart.Timeout, iw.Smart.UseSudo, iw.PathSmartctl, "--scan")
	if err != nil {
		return err
	}

	var devices []string

	for _, line := range strings.Split(string(out), "\n") {
		devWithType := strings.SplitN(line, " ", 2)
		if len(devWithType) <= 1 {
			continue
		}

		if dev := strings.TrimSpace(devWithType[0]); iw.shouldHandleDevice(dev) {
			devices = append(devices, dev+deviceTypeFor(devWithType[1]))
		}
	}

	iw.Smart.Devices = devices

	return iw.Smart.Gather(acc)
}

func deviceTypeFor(devType string) string {
	if !strings.HasPrefix(devType, "-d ") {
		return ""
	}

	// Preventing some device types to be specified
	switch typ := strings.Split(devType, " ")[1]; typ {
	case "":
		return ""
	case "scsi":
		return ""
	default:
		return " -d " + typ
	}
}

func (iw *inputWrapper) shouldHandleDevice(device string) bool {
	if len(iw.devices) != 0 {
		for _, dev := range iw.devices {
			if device == dev {
				return true
			}
		}

		return false
	}

	for _, excluded := range iw.Smart.Excludes {
		if device == excluded {
			return false
		}
	}

	return true
}
