# Details about testing with vSphere

## Generating test data

1. Get a running ESXI/vSphere (with vCenter)/vcsim
2. Dump its structure using the `govc` utility:
   ```shell
   ~/go/bin/govc object.save -u='https://<user>:<passwd>@<host>/sdk' -k -d <output dir>
   ```
3. Remove the files that aren't needed to run our tests, and only keep:
    - ServiceInstance
    - OptionManager
    - ViewManager
    - EventManager
    - Folder-*
    - PerformanceManager
    - PropertyCollector
    - SessionManager
    - Datacenter-*
    - ComputeResource-*
    - HostSystem-*
    - VirtualMachine-*
    - ResourcePool-*
4. Additionally, some parts of some files can be taken off, e.g., the history of redundant events in the EventManager or
   SessionManager. It prevents from versioning thousands of useless XML lines.
   For device description files (HostSystem-*, ...), only the properties referenced in `properties.go` can be kept.


## Generating data from govc dump

If you want to dump a real vSphere but don't commit all information, the best solution is:
* Copy from testdata/vcenter_1 the file that correspond to resource you want to dump (e.g. copy the virtualmachine and hostsystem xml file).
* You might need to copy some parent XML also. For instance resource group require the cluster compute resource
* Then run govc object.save as described previously. Finally manually copy value from the object.save folder to copied XML file.
  Modify value when needed.
