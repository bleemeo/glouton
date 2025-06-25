# Command to produce testdata:

* `index.yml`: This file is manually created. It contains list of supported smartctl output supported per device
  and the return code of this smartctl.
  It should usually containers /dev/sda, some /dev/sgN. It depends on test case (you should include all devices
  required to run the tests).
* `smartctl_scan.txt` is just the output from `smartctl --scan`
* This isn't in testdata itself, but in test Go code, but the available /dev/sgN (`ls /dev/sg*`) might be needed.
* Each file referenced in `index.yml` are produced by:
  ```
  smartctl --info --health --attributes --tolerance=verypermissive -n standby --format=brief $DEVICE
  ```
  Remember to take the return code (`echo $?`) of the command.
  /!\ Remember to anonymize it, remove serial number and unique identifier. You can kept model number.

## Example of proxmox1

The index.yml contains:
```
device_scan:
  /dev/sda:
    file: smartctl_sda.txt
    rc: 2
[...]
  /dev/bus/0 -d megaraid,0:
    file: smartctl_disk0.txt
    rc: 0
```

So we done two smartctl command, which are:
```
smartctl --info --health --attributes --tolerance=verypermissive -n standby --format=brief /dev/sda > smartctl_sda.txt 2>&1
echo $?  # This is `rc: 2` in index.yml
$EDITOR smartctl_sda.txt # Remove serial number & unique identifier

smartctl --info --health --attributes --tolerance=verypermissive -n standby --format=brief /dev/bus/0 -d megaraid,0 > smartctl_disk0.txt 2>&1
echo $?  # This is `rc: 0` in index.yml
$EDITOR smartctl_disk0.txt # Remove serial number & unique identifier
```
