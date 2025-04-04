# Details about testing with vSphere

## Generating test data

1. Get a running ESXI/vSphere (with vCenter)/vcsim, e.g.:
   ```shell
   docker run --rm -it -p 8989:8989 -v ./inputs/vsphere/testdata/esxi_1:/tmp/esxi_1 vmware/vcsim -load /tmp/esxi_1 -l 0.0.0.0:8989
   ```
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
    - (Cluster)ComputeResource-*
    - HostSystem-*
    - VirtualMachine-*
    - ResourcePool-*
4. Additionally, some parts of some files can be taken off, e.g., the history of redundant events in the EventManager,
   PerformanceManager or SessionManager. It prevents from versioning thousands of useless XML lines.
   For device description files (HostSystem-*, ...), only the properties referenced in `properties.go` can be kept
   (+ some properties used by govmomi, like `name`, `parent`, ...).

## Generating data from govc dump

If you want to dump a real vSphere but don't commit all information, the best solution is:

* Copy from testdata/vcenter_1 the file that corresponds to the resource you want to dump
  (e.g., copy the VirtualMachine and HostSystem XML files).
* You might also need to copy some parent XML. For instance, a ResourcePool requires the ClusterComputeResource.
* Then run `govc object.save` as described previously.
  Finally, manually copy values from the `object.save` folder to the copied XML file.
  Modify values when needed.
