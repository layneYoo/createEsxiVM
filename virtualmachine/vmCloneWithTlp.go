package virtualmachine

import (
	"fmt"
	"log"
	"net"
	"regexp"
	"strings"
	"time"

	"xlei/vm/g"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/guest"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	//"github.com/vmware/govmomi/vim25"
	"github.com/Masterminds/glide/msg"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	"golang.org/x/net/context"
)

// addHardDisk adds a new Hard Disk to the VirtualMachine.
func addHardDisk(vm *object.VirtualMachine, size, iops int64, diskType string, datastore *object.Datastore) error {
	devices, err := vm.Device(context.TODO())
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] vm devices: %#v\n", devices)

	controller, err := devices.FindDiskController("scsi")
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] disk controller: %#v\n", controller)

	disk := devices.CreateDisk(controller, datastore.Reference(), "")
	existing := devices.SelectByBackingInfo(disk.Backing)
	log.Printf("[DEBUG] disk: %#v\n", disk)

	if len(existing) == 0 {
		disk.CapacityInKB = int64(size * 1024 * 1024)
		if iops != 0 {
			disk.StorageIOAllocation = &types.StorageIOAllocationInfo{
				Limit: iops,
			}
		}
		backing := disk.Backing.(*types.VirtualDiskFlatVer2BackingInfo)

		if diskType == "eager_zeroed" {
			// eager zeroed thick virtual disk
			backing.ThinProvisioned = types.NewBool(false)
			backing.EagerlyScrub = types.NewBool(true)
		} else if diskType == "thin" {
			// thin provisioned virtual disk
			backing.ThinProvisioned = types.NewBool(true)
		}

		log.Printf("[DEBUG] addHardDisk: %#v\n", disk)
		log.Printf("[DEBUG] addHardDisk: %#v\n", disk.CapacityInKB)

		return vm.AddDevice(context.TODO(), disk)
	} else {
		log.Printf("[DEBUG] addHardDisk: Disk already present.\n")

		return nil
	}
}

// buildNetworkDevice builds VirtualDeviceConfigSpec for Network Device.
func buildNetworkDevice(f *find.Finder, label, adapterType string) (*types.VirtualDeviceConfigSpec, error) {
	//network, err := f.Network(context.TODO(), "*"+label)
	network, err := f.DefaultNetwork(context.TODO())
	if err != nil {
		return nil, err
	}

	backing, err := network.EthernetCardBackingInfo(context.TODO())
	if err != nil {
		return nil, err
	}

	// add virtualdeviceconnectinfo
	//for _, customnetwork := range network {
	//	fmt.Println(customnetwork.GetVirtualDevice())
	//}

	if adapterType == "vmxnet3" {
		return &types.VirtualDeviceConfigSpec{
			Operation: types.VirtualDeviceConfigSpecOperationAdd,
			Device: &types.VirtualVmxnet3{
				VirtualVmxnet: types.VirtualVmxnet{
					VirtualEthernetCard: types.VirtualEthernetCard{
						VirtualDevice: types.VirtualDevice{
							Key:     -1,
							Backing: backing,
						},
						AddressType: string(types.VirtualEthernetCardMacTypeGenerated),
					},
				},
			},
		}, nil
	} else if adapterType == "e1000" {
		return &types.VirtualDeviceConfigSpec{
			Operation: types.VirtualDeviceConfigSpecOperationAdd,
			Device: &types.VirtualE1000{
				VirtualEthernetCard: types.VirtualEthernetCard{
					VirtualDevice: types.VirtualDevice{
						Key:     -1,
						Backing: backing,
					},
					AddressType: string(types.VirtualEthernetCardMacTypeGenerated),
				},
			},
		}, nil
	} else {
		return nil, fmt.Errorf("Invalid network adapter type.")
	}
}

// buildVMRelocateSpec builds VirtualMachineRelocateSpec to set a place for a new VirtualMachine.
func buildVMRelocateSpec(rp *object.ResourcePool, ds *object.Datastore, vm *object.VirtualMachine, initType string) (types.VirtualMachineRelocateSpec, error) {
	var key int

	devices, err := vm.Device(context.TODO())
	if err != nil {
		return types.VirtualMachineRelocateSpec{}, err
	}
	for _, d := range devices {
		if devices.Type(d) == "disk" {
			key = d.GetVirtualDevice().Key
		}
	}

	isThin := initType == "thin"
	rpr := rp.Reference()
	dsr := ds.Reference()
	// TODO : add host
	return types.VirtualMachineRelocateSpec{
		Datastore: &dsr,
		Pool:      &rpr,
		Disk: []types.VirtualMachineRelocateSpecDiskLocator{
			types.VirtualMachineRelocateSpecDiskLocator{
				Datastore: dsr,
				DiskBackingInfo: &types.VirtualDiskFlatVer2BackingInfo{
					DiskMode:        "persistent",
					ThinProvisioned: types.NewBool(isThin),
					EagerlyScrub:    types.NewBool(!isThin),
				},
				DiskId: key,
			},
		},
	}, nil
}

// getDatastoreObject gets datastore object.
func getDatastoreObject(client *govmomi.Client, f *object.DatacenterFolders, name string) (types.ManagedObjectReference, error) {
	s := object.NewSearchIndex(client.Client)
	ref, err := s.FindChild(context.TODO(), f.DatastoreFolder, name)
	if err != nil {
		return types.ManagedObjectReference{}, err
	}
	if ref == nil {
		return types.ManagedObjectReference{}, fmt.Errorf("Datastore '%s' not found.", name)
	}
	log.Printf("[DEBUG] getDatastoreObject: reference: %#v", ref)
	return ref.Reference(), nil
}

// getVmGuestInfo get guest information.
func getVmGuestInfo(client *govmomi.Client, vm types.ManagedObjectReference) (types.GuestInfo, error) {

	var mvm mo.VirtualMachine

	collector := property.DefaultCollector(client.Client)
	if err := collector.RetrieveOne(context.TODO(), vm, []string{"guest"}, &mvm); err != nil {
		return types.GuestInfo{}, err
	}

	return *mvm.Guest, nil
}

// buildStoragePlacementSpecCreate builds StoragePlacementSpec for create action.
func buildStoragePlacementSpecCreate(f *object.DatacenterFolders, rp *object.ResourcePool, storagePod object.StoragePod, configSpec types.VirtualMachineConfigSpec) types.StoragePlacementSpec {
	vmfr := f.VmFolder.Reference()
	rpr := rp.Reference()
	spr := storagePod.Reference()

	sps := types.StoragePlacementSpec{
		Type:       "create",
		ConfigSpec: &configSpec,
		PodSelectionSpec: types.StorageDrsPodSelectionSpec{
			StoragePod: &spr,
		},
		Folder:       &vmfr,
		ResourcePool: &rpr,
	}
	log.Printf("[DEBUG] findDatastore: StoragePlacementSpec: %#v\n", sps)
	return sps
}

// buildStoragePlacementSpecClone builds StoragePlacementSpec for clone action.
func buildStoragePlacementSpecClone(c *govmomi.Client, f *object.DatacenterFolders, vm *object.VirtualMachine, rp *object.ResourcePool, storagePod object.StoragePod) types.StoragePlacementSpec {
	vmr := vm.Reference()
	vmfr := f.VmFolder.Reference()
	rpr := rp.Reference()
	spr := storagePod.Reference()

	var o mo.VirtualMachine
	err := vm.Properties(context.TODO(), vmr, []string{"datastore"}, &o)
	if err != nil {
		return types.StoragePlacementSpec{}
	}
	ds := object.NewDatastore(c.Client, o.Datastore[0])
	log.Printf("[DEBUG] findDatastore: datastore: %#v\n", ds)

	devices, err := vm.Device(context.TODO())
	if err != nil {
		return types.StoragePlacementSpec{}
	}

	var key int
	for _, d := range devices.SelectByType((*types.VirtualDisk)(nil)) {
		key = d.GetVirtualDevice().Key
		log.Printf("[DEBUG] findDatastore: virtual devices: %#v\n", d.GetVirtualDevice())
	}

	sps := types.StoragePlacementSpec{
		Type: "clone",
		Vm:   &vmr,
		PodSelectionSpec: types.StorageDrsPodSelectionSpec{
			StoragePod: &spr,
		},
		CloneSpec: &types.VirtualMachineCloneSpec{
			Location: types.VirtualMachineRelocateSpec{
				Disk: []types.VirtualMachineRelocateSpecDiskLocator{
					types.VirtualMachineRelocateSpecDiskLocator{
						Datastore:       ds.Reference(),
						DiskBackingInfo: &types.VirtualDiskFlatVer2BackingInfo{},
						DiskId:          key,
					},
				},
				Pool: &rpr,
			},
			PowerOn:  false,
			Template: false,
		},
		CloneName: "dummy",
		Folder:    &vmfr,
	}
	return sps
}

// findDatastore finds Datastore object.
func findDatastore(c *govmomi.Client, sps types.StoragePlacementSpec) (*object.Datastore, error) {
	var datastore *object.Datastore
	log.Printf("[DEBUG] findDatastore: StoragePlacementSpec: %#v\n", sps)

	srm := object.NewStorageResourceManager(c.Client)
	rds, err := srm.RecommendDatastores(context.TODO(), sps)
	if err != nil {
		return nil, err
	}
	log.Printf("[DEBUG] findDatastore: recommendDatastores: %#v\n", rds)

	spa := rds.Recommendations[0].Action[0].(*types.StoragePlacementAction)
	datastore = object.NewDatastore(c.Client, spa.Destination)
	log.Printf("[DEBUG] findDatastore: datastore: %#v", datastore)

	return datastore, nil
}

// createVirtualMachine creates a new VirtualMachine.
func (vm *virtualMachine) createVirtualMachine(c *govmomi.Client) error {
	dc, err := getDatacenter(c, vm.datacenter)

	if err != nil {
		return err
	}
	finder := find.NewFinder(c.Client, true)
	finder = finder.SetDatacenter(dc)

	var resourcePool *object.ResourcePool
	if vm.resourcePool == "" {
		if vm.cluster == "" {
			resourcePool, err = finder.DefaultResourcePool(context.TODO())
			if err != nil {
				return err
			}
		} else {
			resourcePool, err = finder.ResourcePool(context.TODO(), "*"+vm.cluster+"/Resources")
			if err != nil {
				return err
			}
		}
	} else {
		resourcePool, err = finder.ResourcePool(context.TODO(), vm.resourcePool)
		if err != nil {
			return err
		}
	}
	log.Printf("[DEBUG] resource pool: %#v", resourcePool)

	dcFolders, err := dc.Folders(context.TODO())
	if err != nil {
		return err
	}

	log.Printf("[DEBUG] folder: %#v", vm.folder)
	folder := dcFolders.VmFolder
	if len(vm.folder) > 0 {
		si := object.NewSearchIndex(c.Client)
		folderRef, err := si.FindByInventoryPath(
			context.TODO(), fmt.Sprintf("%v/vm/%v", vm.datacenter, vm.folder))
		if err != nil {
			return fmt.Errorf("Error reading folder %s: %s", vm.folder, err)
		} else if folderRef == nil {
			return fmt.Errorf("Cannot find folder %s", vm.folder)
		} else {
			folder = folderRef.(*object.Folder)
		}
	}

	// network
	networkDevices := []types.BaseVirtualDeviceConfigSpec{}
	for _, network := range vm.networkInterfaces {
		// network device
		nd, err := buildNetworkDevice(finder, network.label, "e1000")
		if err != nil {
			return err
		}
		networkDevices = append(networkDevices, nd)
	}

	// make config spec
	configSpec := types.VirtualMachineConfigSpec{
		GuestId:           "otherLinux64Guest",
		Name:              vm.name,
		NumCPUs:           vm.vcpu,
		NumCoresPerSocket: 1,
		MemoryMB:          vm.memoryMb,
		DeviceChange:      networkDevices,
	}
	log.Printf("[DEBUG] virtual machine config spec: %v", configSpec)

	// make ExtraConfig
	log.Printf("[DEBUG] virtual machine Extra Config spec start")
	if len(vm.customConfigurations) > 0 {
		var ov []types.BaseOptionValue
		for k, v := range vm.customConfigurations {
			key := k
			value := v
			o := types.OptionValue{
				Key:   key,
				Value: &value,
			}
			log.Printf("[DEBUG] virtual machine Extra Config spec: %s,%s", k, v)
			ov = append(ov, &o)
		}
		configSpec.ExtraConfig = ov
		log.Printf("[DEBUG] virtual machine Extra Config spec: %v", configSpec.ExtraConfig)
	}

	var datastore *object.Datastore
	if vm.datastore == "" {
		datastore, err = finder.DefaultDatastore(context.TODO())
		if err != nil {
			return err
		}
	} else {
		datastore, err = finder.Datastore(context.TODO(), vm.datastore)
		if err != nil {
			// TODO: datastore cluster support in govmomi finder function
			d, err := getDatastoreObject(c, dcFolders, vm.datastore)
			if err != nil {
				return err
			}

			if d.Type == "StoragePod" {
				sp := object.StoragePod{
					Folder: object.NewFolder(c.Client, d),
				}
				sps := buildStoragePlacementSpecCreate(dcFolders, resourcePool, sp, configSpec)
				datastore, err = findDatastore(c, sps)
				if err != nil {
					return err
				}
			} else {
				datastore = object.NewDatastore(c.Client, d)
			}
		}
	}

	log.Printf("[DEBUG] datastore: %#v", datastore)

	var mds mo.Datastore
	if err = datastore.Properties(context.TODO(), datastore.Reference(), []string{"name"}, &mds); err != nil {
		return err
	}
	log.Printf("[DEBUG] datastore: %#v", mds.Name)
	scsi, err := object.SCSIControllerTypes().CreateSCSIController("scsi")
	if err != nil {
		log.Printf("[ERROR] %s", err)
	}

	configSpec.DeviceChange = append(configSpec.DeviceChange, &types.VirtualDeviceConfigSpec{
		Operation: types.VirtualDeviceConfigSpecOperationAdd,
		Device:    scsi,
	})
	configSpec.Files = &types.VirtualMachineFileInfo{VmPathName: fmt.Sprintf("[%s]", mds.Name)}

	task, err := folder.CreateVM(context.TODO(), configSpec, resourcePool, nil)
	if err != nil {
		log.Printf("[ERROR] %s", err)
	}

	err = task.Wait(context.TODO())
	if err != nil {
		log.Printf("[ERROR] %s", err)
	}

	newVM, err := finder.VirtualMachine(context.TODO(), vm.Path())
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] new vm: %v", newVM)

	log.Printf("[DEBUG] add hard disk: %v", vm.hardDisks)
	for _, hd := range vm.hardDisks {
		log.Printf("[DEBUG] add hard disk: %v", hd.size)
		log.Printf("[DEBUG] add hard disk: %v", hd.iops)
		err = addHardDisk(newVM, hd.size, hd.iops, "thin", datastore)
		if err != nil {
			return err
		}
	}
	return nil
}

// deployVirtualMachine deploys a new VirtualMachine.
func (vm *virtualMachine) deployVirtualMachine(c *govmomi.Client) *object.VirtualMachine {
	dc, err := getDatacenter(c, vm.datacenter)
	g.Check(err == nil, "getDatacenter error", err)
	finder := find.NewFinder(c.Client, true)
	finder = finder.SetDatacenter(dc)

	template, err := finder.VirtualMachine(context.TODO(), vm.template)
	g.Check(err == nil, "get vm template error", err)
	log.Printf("[DEBUG] template: %#v", template)

	var resourcePool *object.ResourcePool
	if vm.resourcePool == "" {
		if vm.cluster == "" {
			resourcePool, err = finder.DefaultResourcePool(context.TODO())
			g.Check(err == nil, "get resourcePool error", err)
		} else {
			resourcePool, err = finder.ResourcePool(context.TODO(), "*"+vm.cluster+"/Resources")
			g.Check(err == nil, "get resourcePool error", err)
		}
	} else {
		resourcePool, err = finder.ResourcePool(context.TODO(), vm.resourcePool)
		g.Check(err == nil, "get resourcePool error", err)
	}
	log.Printf("[DEBUG] resource pool: %#v", resourcePool)

	dcFolders, err := dc.Folders(context.TODO())
	g.Check(err == nil, "get dcFolder error", err)

	log.Printf("[DEBUG] folder: %#v", vm.folder)
	folder := dcFolders.VmFolder
	if len(vm.folder) > 0 {
		si := object.NewSearchIndex(c.Client)
		folderRef, err := si.FindByInventoryPath(
			context.TODO(), fmt.Sprintf("%v/vm/%v", vm.datacenter, vm.folder))
		if err != nil {
			//return fmt.Errorf("Error reading folder %s: %s", vm.folder, err)
			fmt.Errorf("Error reading folder %s: %s", vm.folder, err)
		} else if folderRef == nil {
			//return fmt.Errorf("Cannot find folder %s", vm.folder)
			fmt.Errorf("Cannot find folder %s", vm.folder)
		} else {
			folder = folderRef.(*object.Folder)
		}
	}

	var datastore *object.Datastore
	if vm.datastore == "" {
		datastore, err = finder.DefaultDatastore(context.TODO())
		g.Check(err == nil, "492 : get datastore error", err)
	} else {
		datastore, err = finder.Datastore(context.TODO(), vm.datastore)
		if err != nil {
			// TODO: datastore cluster support in govmomi finder function
			d, err := getDatastoreObject(c, dcFolders, vm.datastore)
			g.Check(err == nil, "498 : get datastore object error", err)

			if d.Type == "StoragePod" {
				sp := object.StoragePod{
					Folder: object.NewFolder(c.Client, d),
				}
				sps := buildStoragePlacementSpecClone(c, dcFolders, template, resourcePool, sp)

				datastore, err = findDatastore(c, sps)
				g.Check(err == nil, "507 : find datastore error", err)
			} else {
				datastore = object.NewDatastore(c.Client, d)
			}
		}
	}

	log.Printf("[DEBUG] datastore: %#v", datastore)

	relocateSpec, err := buildVMRelocateSpec(resourcePool, datastore, template, vm.hardDisks[0].initType)
	g.Check(err == nil, "517 : buildVMRelocateSpec error", err)

	log.Printf("[DEBUG] relocate spec: %v", relocateSpec)

	// network
	networkDevices := []types.BaseVirtualDeviceConfigSpec{}
	networkConfigs := []types.CustomizationAdapterMapping{}
	for _, network := range vm.networkInterfaces {
		// network device
		nd, err := buildNetworkDevice(finder, network.label, "vmxnet3")
		g.Check(err == nil, "527 : buildNetworkDevice error", err)
		networkDevices = append(networkDevices, nd)

		// TODO: IPv6 support
		var ipSetting types.CustomizationIPSettings
		if network.ipv4Address == "" {
			ipSetting = types.CustomizationIPSettings{
				Ip: &types.CustomizationDhcpIpGenerator{},
			}
		} else {
			if network.ipv4PrefixLength == 0 {
				//return fmt.Errorf("Error: ipv4_prefix_length argument is empty.")
				fmt.Errorf("Error: ipv4_prefix_length argument is empty.")
			}
			m := net.CIDRMask(network.ipv4PrefixLength, 32)
			sm := net.IPv4(m[0], m[1], m[2], m[3])
			subnetMask := sm.String()
			log.Printf("[DEBUG] gateway: %v", vm.gateway)
			log.Printf("[DEBUG] ipv4 address: %v", network.ipv4Address)
			log.Printf("[DEBUG] ipv4 prefix length: %v", network.ipv4PrefixLength)
			log.Printf("[DEBUG] ipv4 subnet mask: %v", subnetMask)
			ipSetting = types.CustomizationIPSettings{
				Gateway: []string{
					vm.gateway,
				},
				Ip: &types.CustomizationFixedIp{
					IpAddress: network.ipv4Address,
				},
				SubnetMask: subnetMask,
			}
		}

		// network config
		config := types.CustomizationAdapterMapping{
			Adapter: ipSetting,
		}
		networkConfigs = append(networkConfigs, config)
	}
	log.Printf("[DEBUG] network configs: %v", networkConfigs[0].Adapter)

	// make config spec
	configSpec := types.VirtualMachineConfigSpec{
		NumCPUs:           vm.vcpu,
		NumCoresPerSocket: 1,
		MemoryMB:          vm.memoryMb,
		DeviceChange:      networkDevices,
	}
	log.Printf("[DEBUG] virtual machine config spec: %v", configSpec)

	log.Printf("[DEBUG] starting extra custom config spec: %v", vm.customConfigurations)

	// make ExtraConfig
	if len(vm.customConfigurations) > 0 {
		fmt.Println("=================+++++++++++++++++++==========")
		var ov []types.BaseOptionValue
		for k, v := range vm.customConfigurations {
			fmt.Println("==============", k, v)
			key := k
			value := v
			o := types.OptionValue{
				Key:   key,
				Value: &value,
			}
			ov = append(ov, &o)
		}
		configSpec.ExtraConfig = ov
		log.Printf("[DEBUG] virtual machine Extra Config spec: %v", configSpec.ExtraConfig)
	}

	// create CustomizationSpec
	customSpec := types.CustomizationSpec{
		Identity: &types.CustomizationLinuxPrep{
			HostName: &types.CustomizationFixedName{
				Name: strings.Split(vm.name, ".")[0],
			},
			Domain:     vm.domain,
			TimeZone:   vm.timeZone,
			HwClockUTC: types.NewBool(true),
		},
		GlobalIPSettings: types.CustomizationGlobalIPSettings{
			DnsSuffixList: vm.dnsSuffixes,
			DnsServerList: vm.dnsServers,
		},
		//NicSettingMap: []types.CustomizationAdapterMapping{},
		NicSettingMap: networkConfigs,
	}

	// get the guest info
	guestInfo, err := getVmGuestInfo(c, template.Reference())
	g.Check(err == nil, "615 : getVmGuestInfo error", err)

	// check for windows guest
	windowsGuest, err := regexp.MatchString("windows", guestInfo.GuestId)
	log.Printf("[DEBUG] guestId: %v", guestInfo.GuestId)

	if windowsGuest {
		log.Printf("[DEBUG] isWindows")
	}

	if vm.customizationSpecification != nil {

		if v, ok := vm.customizationSpecification["name"]; ok {
			specManager := object.NewCustomizationSpecManager(c.Client)
			specItem, err := specManager.GetCustomizationSpec(context.TODO(), v.(string))
			g.Check(err == nil, "630 : specManager.GetCustomizationSpec error", err)
			customSpec = specItem.Spec
		}

	}

	log.Printf("[DEBUG] custom spec: %v", customSpec)

	// make vm clone spec
	cloneSpec := types.VirtualMachineCloneSpec{
		Location: relocateSpec,
		Template: false,
		Config:   &configSpec,
		PowerOn:  false,
	}
	log.Printf("[DEBUG] clone spec: %v", cloneSpec)

	task, err := template.Clone(context.TODO(), folder, vm.name, cloneSpec)
	g.Check(err == nil, "648 : template clone error", err)

	_, err = task.WaitForResult(context.TODO(), nil)
	g.Check(err == nil, "651 : clone task error", err)

	newVM, err := finder.VirtualMachine(context.TODO(), vm.Path())
	g.Check(err == nil, "657 : find virtual machine error", err)
	log.Printf("[DEBUG] new vm: %v", newVM)

	devices, err := newVM.Device(context.TODO())
	g.Check(err == nil, "658 : new vm device can not be found", err)

	for _, dvc := range devices {
		// Issue 3559/3560: Delete all ethernet devices to add the correct ones later
		if devices.Type(dvc) == "ethernet" {
			err := newVM.RemoveDevice(context.TODO(), dvc)
			g.Check(err == nil, "664 : new vm remove device error", err)
		}
	}
	// Add Network devices
	for _, dvc := range networkDevices {
		err := newVM.AddDevice(
			context.TODO(), dvc.GetVirtualDeviceConfigSpec().Device)
		g.Check(err == nil, "671 : new vm add device error", err)
	}

	// power on the newVM
	newVM.PowerOn(context.TODO())

	return newVM
	// device test
	//deviceList, err := newVM.Device(context.TODO())
	//if err != nil {
	//	fmt.Println(err.Error)
	//	return nil
	//}
	//for _, device := range deviceList {
	//	fmt.Printf("%s\t%s\t%s\n", devices.Name(device), devices.TypeName(device),
	//		device.GetVirtualDevice().DeviceInfo.GetDescription().Summary)
	//}
	//eth0 := deviceList.Find("ethernet-0")
	//currenteth0 := eth0.(types.BaseVirtualEthernetCard).GetVirtualEthernetCard()
	//fmt.Println(currenteth0.Connectable.StartConnected)

	/*
		taskb, err := newVM.Customize(context.TODO(), customSpec)
		if err != nil {
			return err
		}

		_, err = taskb.WaitForResult(context.TODO(), nil)
		if err != nil {
			return err
		}
		log.Printf("[DEBUG]VM customization finished")
		//return nil

		for i := 1; i < len(vm.hardDisks); i++ {
			err = addHardDisk(newVM, vm.hardDisks[i].size, vm.hardDisks[i].iops, vm.hardDisks[i].initType, datastore)
			if err != nil {
				return err
			}
		}
		log.Printf("[DEBUG] virtual machine config spec: %v", configSpec)

		taskv, err := newVM.PowerOn(context.TODO())
		if err != nil {
			return err
		}
		_, err = taskv.WaitForResult(context.TODO(), nil)
		if err != nil {
			return err
		}
		//loginVM(newVM.Client(), newVM.Reference())
		//newVM.PowerOn(context.TODO())
		return nil

		ip, err := newVM.WaitForIP(context.TODO())
		if err != nil {
			return err
		}
		log.Printf("[DEBUG] ip address: %v", ip)

		return nil
	*/
}

// getDatacenter gets datacenter object
func getDatacenter(c *govmomi.Client, dc string) (*object.Datacenter, error) {
	finder := find.NewFinder(c.Client, true)
	if dc != "" {
		d, err := finder.Datacenter(context.TODO(), dc)
		return d, err
	} else {
		d, err := finder.DefaultDatacenter(context.TODO())
		return d, err
	}
}

func DeployVMs(vm *virtualMachine, client *govmomi.Client) *object.VirtualMachine {
	vmClient := vm.deployVirtualMachine(client)
	g.Check(vmClient != nil, "deploy the vm error", nil)
	return vmClient
}

// login vm and process
func VmProcess(client *govmomi.Client, vmpath string) {
	var auth types.NamePasswordAuthentication
	auth.Username = "root"
	auth.Password = "8bio8cwa"

	finder := find.NewFinder(client.Client, true)
	vm, err := finder.VirtualMachine(context.TODO(), vmpath)
	g.Check(err == nil, "find vm guest instance error", err)

	o := guest.NewOperationsManager(vm.Client(), vm.Reference())
	pro, err := o.ProcessManager(context.TODO())
	g.Check(err == nil, "processor manager error", err)

	// check vm power
	powerstate, err := vm.PowerState(context.TODO())
	g.Check(err == nil, "get vm guest power status failed", err)
	for {
		if powerstate != "poweredOn" {
			time.Sleep(time.Second)
			msg.Info("Wait for power on")
			powerstate, err = vm.PowerState(context.TODO())
			g.Check(err == nil, "get vm guest power status failed", err)
		} else {
			break
		}
	}

	// check vmware tools
	ret, err := vm.IsToolsRunning(context.TODO())
	g.Check(err == nil, "get vmwaretool status error", err)
	for {
		if ret != true {
			time.Sleep(time.Second)
			msg.Info("wait, the vmware tools not running...")
			ret, err = vm.IsToolsRunning(context.TODO())
			g.Check(err == nil, "get vmwaretool status error", err)
		} else {
			break
		}
	}

	spec := types.GuestProgramSpec{
		ProgramPath:      "/bin/sed",
		Arguments:        "-i 's/10.10.221.89/10.10.221.90/g' /etc/sysconfig/network-scripts/ifcfg-eth0",
		WorkingDirectory: "/",
		EnvVariables:     []string{},
	}
	_, err = pro.StartProgram(context.TODO(), &auth, &spec)
	g.Check(err == nil, "vm guest start program error", err)
	err = vm.RebootGuest(context.TODO())
	g.Check(err == nil, "vm guest reboot error", err)
	IP, err := vm.WaitForIP(context.TODO())
	msg.Info(IP)
}
