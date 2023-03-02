package config

import (
	"glouton/prometheus/exporter/common"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// TestDiskIgnore check that disk ignore regexp match expected disks.
func TestDefaultDiskIgnore(t *testing.T) {
	cfg := DefaultConfig()

	denylistRE, err := common.CompileREs(cfg.DiskIgnore)
	if err != nil {
		t.Fatal(err)
	}

	allowedDisk := []string{
		"C:",
		"disk0", // Value seen on MacOS
		"drbd0",
		"fioa",
		"hda",
		"md0",
		"md127",
		"mmcblk0",
		"nbd0", // Network block device, usually qemu-nbd
		"nbd15",
		"nvme0c0n1",
		"nvme0n1",
		"rbd0",
		"rssda",
		"rsxx0",
		"sda",
		"sdaa",
		"sdcr",
		"sdz",
		"skd0",
		"vda",
		"xvda",

		// Edge case. We probably should like to ignore this, but it seems a
		// really specific situation.
		"mmcblk2boot1",
	}

	deniedDisk := []string{
		"bcache0",
		"dm-0",
		"drbd0p1",
		"fd0",
		"fioa1",
		"hda1",
		"loop0",
		"md102p2",
		"mmcblk0p1",
		"nbd0p1",
		"nbd0p15",
		"nbd15p12",
		"nvme0n1p1",
		"ram0",
		"rbd0p1",
		"rssda1",
		"rsxx0p1",
		"sda1",
		"sdaa1",
		"sdaa10",
		"sdba10",
		"skd0p1",
		"sr0", // SCSI cdrom
		"vda1",
		"xvda1",
		"zd0", // zs are ZFS volume
		"zd48",
		"zram0", // compressed tmpfs, usually for compressed swap
	}

	allDisk := make([]string, 0, len(allowedDisk)+len(deniedDisk))
	allDisk = append(allDisk, allowedDisk...)
	allDisk = append(allDisk, deniedDisk...)

	for i, disk := range allDisk {
		i := i
		disk := disk

		t.Run(disk, func(t *testing.T) {
			t.Parallel()

			match := true

			for _, r := range denylistRE {
				if r.MatchString(disk) {
					match = false

					break
				}
			}

			shouldMatch := i < len(allowedDisk)

			if match != shouldMatch {
				t.Errorf("Disk %s is allowed=%v, want %v", disk, match, shouldMatch)
			}
		})
	}
}

// TestLoadFile check that loading the etc/glouton.conf file works and yield the default settings.
func TestLoadFile(t *testing.T) {
	cfg, warnings, err := load(&configLoader{}, true, false, "../etc/glouton.conf")
	if err != nil {
		t.Fatal(err)
	}

	if len(warnings) > 0 {
		t.Errorf("config had warning: %v", warnings)
	}

	defaultCfg := DefaultConfig()
	// There is few settings that aren't in built-in default:
	// * thershold: most likely for historical reason, we should probably comment
	//   the settings in glouton.conf.
	defaultCfg.Thresholds["cpu_used"] = Threshold{
		HighWarning:  newFloatPointer(80),
		HighCritical: newFloatPointer(90),
	}
	defaultCfg.Thresholds["disk_used_perc"] = Threshold{
		HighWarning:  newFloatPointer(80),
		HighCritical: newFloatPointer(90),
	}
	defaultCfg.Thresholds["mem_used_perc"] = Threshold{
		HighWarning:  newFloatPointer(80),
		HighCritical: newFloatPointer(90),
	}
	defaultCfg.Thresholds["io_utilization"] = Threshold{
		HighWarning:  newFloatPointer(80),
		HighCritical: newFloatPointer(90),
	}

	if diff := cmp.Diff(defaultCfg, cfg, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("Default value modified:\n%s", diff)
	}
}
