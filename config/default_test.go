package config

import (
	"glouton/prometheus/exporter/common"
	"testing"
)

// TestDiskIgnore check that disk ignore regexp match expected disks.
func TestDefaultDiskIgnore(t *testing.T) {
	cfg := DefaultConfig()

	denylistRE, err := common.CompileREs(cfg.DiskIgnore)
	if err != nil {
		t.Fatal(err)
	}

	allowedDisk := []string{
		"bcache0",
		"C:",
		"disk0", // Value seen on MacOS
		"drbd0",
		"fioa",
		"hda",
		"md0",
		"md102p2",
		"md127",
		"mmcblk0",
		"mmcblk2boot1",
		"nbd0", // Network block device, usually qemu-nbd
		"nbd0p1",
		"nbd0p15",
		"nbd15",
		"nbd15p12",
		"nvme0c0n1",
		"nvme0n1",
		"rbd0",
		"rssda",
		"rsxx0",
		"sda",
		"sdaa",
		"sdaa1",
		"sdaa10",
		"sdba10",
		"sdcr",
		"sdz",
		"skd0",
		"sr0", // SCSI cdrom
		"vda",
		"xvda",
		"zd0", // zs are ZFS volume
		"zd48",
		"zram0", // compressed tmpfs, usually for compressed swap
	}

	deniedDisk := []string{
		"dm-0",
		"drbd0p1",
		"fd0",
		"fioa1",
		"hda1",
		"loop0",
		"mmcblk0p1",
		"nvme0n1p1",
		"ram0",
		"rbd0p1",
		"rssda1",
		"rsxx0p1",
		"sda1",
		"skd0p1",
		"vda1",
		"xvda1",
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
