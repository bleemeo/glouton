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
    - EventManager
    - Folder-*
    - PropertyCollector
    - SessionManager
    - Datacenter-*
    - ComputeResource-*
    - HostSystem-*
    - VirtualMachine-*
4. Additionally, some parts of some files can be taken off, e.g., the history of redundant events in the EventManager or
   SessionManager. It prevents from versioning thousands of useless XML lines.
