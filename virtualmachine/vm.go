package virtualmachine

import (
	"github.com/vmware/govmomi/vim25/types"
)

var DefaultDNSSuffixes = []string{
	"vsphere.local",
}

var DefaultDNSServers = []string{
	"8.8.8.8",
	"8.8.4.4",
}

type networkInterface struct {
	deviceName       string
	label            string
	ipv4Address      string
	ipv4PrefixLength int
	ipv6Address      string
	ipv6PrefixLength int
	adapterType      string // TODO: Make "adapter_type" argument
}

type hardDisk struct {
	size     int64
	iops     int64
	initType string
}

type virtualMachine struct {
	name                       string
	folder                     string
	datacenter                 string
	cluster                    string
	resourcePool               string
	datastore                  string
	vcpu                       int
	memoryMb                   int64
	template                   string
	networkInterfaces          []networkInterface
	hardDisks                  []hardDisk
	gateway                    string
	domain                     string
	timeZone                   string
	dnsSuffixes                []string
	dnsServers                 []string
	customConfigurations       map[string](types.AnyType)
	customizationSpecification map[string](types.AnyType)
}

type Config struct {
	User          string
	Password      string
	VCenterServer string
}

func (v virtualMachine) Path() string {
	return vmPath(v.folder, v.name)
}

func vmPath(folder string, name string) string {
	var path string
	if len(folder) > 0 {
		path += folder + "/"
	}
	return path + name
}

func CreateVMObj() *virtualMachine {
	var oNet networkInterface
	var ohardDisk hardDisk
	var oVM virtualMachine
	var oNetArr []networkInterface
	var oDiskArr []hardDisk

	oNet.ipv4Address = "10.10.221.90"
	oNet.ipv4PrefixLength = 24
	oNet.label = ""
	//oNet.deviceName = "vmnic0"
	oNet.adapterType = "vmxnet3"

	oNetArr = append(oNetArr, oNet)

	ohardDisk.initType = "thick"
	oDiskArr = append(oDiskArr, ohardDisk)

	oVM.networkInterfaces = oNetArr
	oVM.hardDisks = oDiskArr

	//oVM.template = "tlp_centos6.7"
	oVM.template = "6.7_tlp"
	oVM.gateway = "10.10.221.1"
	oVM.vcpu = 4
	oVM.memoryMb = 4096
	oVM.name = "test"
	oVM.domain = "vmware"
	oVM.dnsServers = []string{"10.103.10.5"}

	return &oVM
}
