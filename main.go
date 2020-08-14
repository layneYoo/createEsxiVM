package main

import (
	"xlei/vm/g"
	vm "xlei/vm/virtualmachine"
)

func main() {
	var vmAuth vm.Config
	vmAuth.User = "root"
	vmAuth.Password = "root"
	vmAuth.VCenterServer = "1.1.1.1"

	client, err := vmAuth.Client()
	g.Check(err == nil, "create vcenter client error", err)

	vmObj := vm.CreateVMObj()
	oVmClient := vm.DeployVMs(vmObj, client)

	vm.VmProcess(client, oVmClient.InventoryPath)

}
