package compute

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2020-06-01/compute"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/helper/validation"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/helpers/suppress"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/clients"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/services/compute/parse"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/services/compute/validate"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/timeouts"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/utils"
)

func virtualMachineAdditionalCapabilitiesSchema() *schema.Schema {
	return &schema.Schema{
		Type:     schema.TypeList,
		Optional: true,
		MaxItems: 1,
		Elem: &schema.Resource{
			Schema: map[string]*schema.Schema{
				// TODO: confirm this command

				// NOTE: requires registration to use:
				// $ az feature show --namespace Microsoft.Compute --name UltraSSDWithVMSS
				// $ az provider register -n Microsoft.Compute
				"ultra_ssd_enabled": {
					Type:     schema.TypeBool,
					Optional: true,
					Default:  false,
				},
			},
		},
	}
}

func expandVirtualMachineAdditionalCapabilities(input []interface{}) *compute.AdditionalCapabilities {
	capabilities := compute.AdditionalCapabilities{}

	if len(input) > 0 {
		raw := input[0].(map[string]interface{})

		capabilities.UltraSSDEnabled = utils.Bool(raw["ultra_ssd_enabled"].(bool))
	}

	return &capabilities
}

func flattenVirtualMachineAdditionalCapabilities(input *compute.AdditionalCapabilities) []interface{} {
	if input == nil {
		return []interface{}{}
	}

	ultraSsdEnabled := false

	if input.UltraSSDEnabled != nil {
		ultraSsdEnabled = *input.UltraSSDEnabled
	}

	return []interface{}{
		map[string]interface{}{
			"ultra_ssd_enabled": ultraSsdEnabled,
		},
	}
}

func virtualMachineIdentitySchema() *schema.Schema {
	return &schema.Schema{
		Type:     schema.TypeList,
		Optional: true,
		MaxItems: 1,
		Elem: &schema.Resource{
			Schema: map[string]*schema.Schema{
				"type": {
					Type:     schema.TypeString,
					Required: true,
					ValidateFunc: validation.StringInSlice([]string{
						string(compute.ResourceIdentityTypeSystemAssigned),
						string(compute.ResourceIdentityTypeUserAssigned),
						string(compute.ResourceIdentityTypeSystemAssignedUserAssigned),
					}, false),
				},

				"identity_ids": {
					Type:     schema.TypeSet,
					Optional: true,
					Elem: &schema.Schema{
						Type: schema.TypeString,
						// TODO: validation for a UAI which requires an ID Parser/Validator
					},
				},

				"principal_id": {
					Type:     schema.TypeString,
					Computed: true,
				},

				"tenant_id": {
					Type:     schema.TypeString,
					Computed: true,
				},
			},
		},
	}
}

func expandVirtualMachineIdentity(input []interface{}) (*compute.VirtualMachineIdentity, error) {
	if len(input) == 0 {
		// TODO: Does this want to be this, or nil?
		return &compute.VirtualMachineIdentity{
			Type: compute.ResourceIdentityTypeNone,
		}, nil
	}

	raw := input[0].(map[string]interface{})

	identity := compute.VirtualMachineIdentity{
		Type: compute.ResourceIdentityType(raw["type"].(string)),
	}

	identityIdsRaw := raw["identity_ids"].(*schema.Set).List()
	identityIds := make(map[string]*compute.VirtualMachineIdentityUserAssignedIdentitiesValue)
	for _, v := range identityIdsRaw {
		identityIds[v.(string)] = &compute.VirtualMachineIdentityUserAssignedIdentitiesValue{}
	}

	if len(identityIds) > 0 {
		if identity.Type != compute.ResourceIdentityTypeUserAssigned && identity.Type != compute.ResourceIdentityTypeSystemAssignedUserAssigned {
			return nil, fmt.Errorf("`identity_ids` can only be specified when `type` includes `UserAssigned`")
		}

		identity.UserAssignedIdentities = identityIds
	}

	return &identity, nil
}

func flattenVirtualMachineIdentity(input *compute.VirtualMachineIdentity) []interface{} {
	if input == nil || input.Type == compute.ResourceIdentityTypeNone {
		return []interface{}{}
	}

	identityIds := make([]string, 0)
	if input.UserAssignedIdentities != nil {
		for k := range input.UserAssignedIdentities {
			identityIds = append(identityIds, k)
		}
	}

	principalId := ""
	if input.PrincipalID != nil {
		principalId = *input.PrincipalID
	}

	tenantId := ""
	if input.TenantID != nil {
		tenantId = *input.TenantID
	}

	return []interface{}{
		map[string]interface{}{
			"type":         string(input.Type),
			"identity_ids": identityIds,
			"principal_id": principalId,
			"tenant_id":    tenantId,
		},
	}
}

func expandVirtualMachineNetworkInterfaceIDs(input []interface{}) []compute.NetworkInterfaceReference {
	output := make([]compute.NetworkInterfaceReference, 0)

	for i, v := range input {
		output = append(output, compute.NetworkInterfaceReference{
			ID: utils.String(v.(string)),
			NetworkInterfaceReferenceProperties: &compute.NetworkInterfaceReferenceProperties{
				Primary: utils.Bool(i == 0),
			},
		})
	}

	return output
}

func flattenVirtualMachineNetworkInterfaceIDs(input *[]compute.NetworkInterfaceReference) []interface{} {
	if input == nil {
		return []interface{}{}
	}

	output := make([]interface{}, 0)

	for _, v := range *input {
		if v.ID == nil {
			continue
		}

		output = append(output, *v.ID)
	}

	return output
}

func virtualMachineOSDiskSchema() *schema.Schema {
	return &schema.Schema{
		Type:     schema.TypeList,
		Required: true,
		MaxItems: 1,
		Elem: &schema.Resource{
			Schema: map[string]*schema.Schema{
				"caching": {
					Type:     schema.TypeString,
					Required: true,
					ValidateFunc: validation.StringInSlice([]string{
						string(compute.CachingTypesNone),
						string(compute.CachingTypesReadOnly),
						string(compute.CachingTypesReadWrite),
					}, false),
				},
				"storage_account_type": {
					Type:     schema.TypeString,
					Required: true,
					// whilst this appears in the Update block the API returns this when changing:
					// Changing property 'osDisk.managedDisk.storageAccountType' is not allowed
					ForceNew: true,
					ValidateFunc: validation.StringInSlice([]string{
						// note: OS Disks don't support Ultra SSDs
						string(compute.StorageAccountTypesPremiumLRS),
						string(compute.StorageAccountTypesStandardLRS),
						string(compute.StorageAccountTypesStandardSSDLRS),
					}, false),
				},

				// Optional
				"diff_disk_settings": {
					Type:     schema.TypeList,
					Optional: true,
					ForceNew: true,
					MaxItems: 1,
					Elem: &schema.Resource{
						Schema: map[string]*schema.Schema{
							"option": {
								Type:     schema.TypeString,
								Required: true,
								ForceNew: true,
								ValidateFunc: validation.StringInSlice([]string{
									string(compute.Local),
								}, false),
							},
						},
					},
				},

				"disk_encryption_set_id": {
					Type:     schema.TypeString,
					Optional: true,
					// the Compute/VM API is broken and returns the Resource Group name in UPPERCASE
					DiffSuppressFunc: suppress.CaseDifference,
					ValidateFunc:     validate.DiskEncryptionSetID,
				},

				"disk_size_gb": {
					Type:         schema.TypeInt,
					Optional:     true,
					Computed:     true,
					ValidateFunc: validation.IntBetween(0, 2048),
				},

				"name": {
					Type:     schema.TypeString,
					Optional: true,
					ForceNew: true,
					Computed: true,
				},

				"write_accelerator_enabled": {
					Type:     schema.TypeBool,
					Optional: true,
					Default:  false,
				},
			},
		},
	}
}

func virtualMachineDataDiskSchema() *schema.Schema {
	return &schema.Schema{
		Type:     schema.TypeList,
		Optional: true,
		//Computed: true,
		Elem: &schema.Resource{
			Schema: map[string]*schema.Schema{
				"caching": {
					Type:     schema.TypeString,
					Required: true,
					ValidateFunc: validation.StringInSlice([]string{
						string(compute.CachingTypesNone),
						string(compute.CachingTypesReadOnly),
						string(compute.CachingTypesReadWrite),
					}, false),
				},

				"lun": {
					Type:         schema.TypeInt,
					Required:     true,
					ValidateFunc: validation.IntAtLeast(0),
				},

				"name": {
					Type:         schema.TypeString,
					Required:     true,
					ValidateFunc: validation.StringIsNotEmpty,
				},

				"storage_account_type": {
					Type:     schema.TypeString,
					Required: true,
					ValidateFunc: validation.StringInSlice([]string{
						string(compute.StandardLRS),
						string(compute.PremiumLRS),
						string(compute.StandardSSDLRS),
						string(compute.UltraSSDLRS),
					}, false),
					DiffSuppressFunc: suppress.CaseDifference,
				},

				// Optional
				"disk_encryption_set_id": {
					Type:         schema.TypeString,
					Optional:     true,
					ValidateFunc: validate.DiskEncryptionSetID,
				},

				"disk_size_gb": {
					Type:         schema.TypeInt,
					Optional:     true,
					Computed:     true,
					ValidateFunc: validateManagedDiskSizeGB,
				},

				"managed_disk_id": {
					Type:     schema.TypeString,
					Optional: true,
					Computed: true,
				},

				"write_accelerator_enabled": {
					Type:     schema.TypeBool,
					Optional: true,
					Default:  false,
				},

				// Computed only
				"create_option": {
					Type:     schema.TypeString,
					Computed: true,
				},

				"disk_iops_read_write": {
					Type:     schema.TypeInt,
					Computed: true,
				},

				"disk_mbps_read_write": {
					Type:     schema.TypeInt,
					Computed: true,
				},
			},
		},
	}
}

func expandVirtualMachineOSDisk(input []interface{}, osType compute.OperatingSystemTypes) *compute.OSDisk {
	raw := input[0].(map[string]interface{})
	disk := compute.OSDisk{
		Caching: compute.CachingTypes(raw["caching"].(string)),
		ManagedDisk: &compute.ManagedDiskParameters{
			StorageAccountType: compute.StorageAccountTypes(raw["storage_account_type"].(string)),
		},
		WriteAcceleratorEnabled: utils.Bool(raw["write_accelerator_enabled"].(bool)),

		// these have to be hard-coded so there's no point exposing them
		// for CreateOption, whilst it's possible for this to be "Attach" for an OS Disk
		// from what we can tell this approach has been superseded by provisioning from
		// an image of the machine (e.g. an Image/Shared Image Gallery)
		CreateOption: compute.DiskCreateOptionTypesFromImage,
		OsType:       osType,
	}

	if osDiskSize := raw["disk_size_gb"].(int); osDiskSize > 0 {
		disk.DiskSizeGB = utils.Int32(int32(osDiskSize))
	}

	if diffDiskSettingsRaw := raw["diff_disk_settings"].([]interface{}); len(diffDiskSettingsRaw) > 0 {
		diffDiskRaw := diffDiskSettingsRaw[0].(map[string]interface{})
		disk.DiffDiskSettings = &compute.DiffDiskSettings{
			Option: compute.DiffDiskOptions(diffDiskRaw["option"].(string)),
		}
	}

	if id := raw["disk_encryption_set_id"].(string); id != "" {
		disk.ManagedDisk.DiskEncryptionSet = &compute.DiskEncryptionSetParameters{
			ID: utils.String(id),
		}
	}

	if name := raw["name"].(string); name != "" {
		disk.Name = utils.String(name)
	}

	return &disk
}

func flattenVirtualMachineOSDisk(ctx context.Context, disksClient *compute.DisksClient, input *compute.OSDisk) ([]interface{}, error) {
	if input == nil {
		return []interface{}{}, nil
	}

	diffDiskSettings := make([]interface{}, 0)
	if input.DiffDiskSettings != nil {
		diffDiskSettings = append(diffDiskSettings, map[string]interface{}{
			"option": string(input.DiffDiskSettings.Option),
		})
	}

	diskSizeGb := 0
	if input.DiskSizeGB != nil && *input.DiskSizeGB != 0 {
		diskSizeGb = int(*input.DiskSizeGB)
	}

	var name string
	if input.Name != nil {
		name = *input.Name
	}

	diskEncryptionSetId := ""
	storageAccountType := ""

	if input.ManagedDisk != nil {
		storageAccountType = string(input.ManagedDisk.StorageAccountType)

		if input.ManagedDisk.ID != nil {
			id, err := parse.ManagedDiskID(*input.ManagedDisk.ID)
			if err != nil {
				return nil, err
			}

			disk, err := disksClient.Get(ctx, id.ResourceGroup, id.Name)
			if err != nil {
				// turns out ephemeral disks aren't returned/available here
				if !utils.ResponseWasNotFound(disk.Response) {
					return nil, err
				}
			}

			// Ephemeral Disks get an ARM ID but aren't available via the regular API
			// ergo fingers crossed we've got it from the resource because ¯\_(ツ)_/¯
			// where else we'd be able to pull it from
			if !utils.ResponseWasNotFound(disk.Response) {
				// whilst this is available as `input.ManagedDisk.StorageAccountType` it's not returned there
				// however it's only available there for ephemeral os disks
				if disk.Sku != nil && storageAccountType == "" {
					storageAccountType = string(disk.Sku.Name)
				}

				// same goes for Disk Size GB apparently
				if diskSizeGb == 0 && disk.DiskProperties != nil && disk.DiskProperties.DiskSizeGB != nil {
					diskSizeGb = int(*disk.DiskProperties.DiskSizeGB)
				}

				// same goes for Disk Encryption Set Id apparently
				if disk.Encryption != nil && disk.Encryption.DiskEncryptionSetID != nil {
					diskEncryptionSetId = *disk.Encryption.DiskEncryptionSetID
				}
			}
		}
	}

	writeAcceleratorEnabled := false
	if input.WriteAcceleratorEnabled != nil {
		writeAcceleratorEnabled = *input.WriteAcceleratorEnabled
	}
	return []interface{}{
		map[string]interface{}{
			"caching":                   string(input.Caching),
			"disk_size_gb":              diskSizeGb,
			"diff_disk_settings":        diffDiskSettings,
			"disk_encryption_set_id":    diskEncryptionSetId,
			"name":                      name,
			"storage_account_type":      storageAccountType,
			"write_accelerator_enabled": writeAcceleratorEnabled,
		},
	}, nil
}

func expandVirtualMachineDataDisks(d *schema.ResourceData, meta interface{}) (*[]compute.DataDisk, error) {
	disksClient := meta.(*clients.Client).Compute.DisksClient
	ctx, cancel := timeouts.ForRead(meta.(*clients.Client).StopContext, d)
	defer cancel()

	result := make([]compute.DataDisk, 0)

	dataDisksRaw, ok := d.GetOk("data_disk")

	if !ok {
		return &result, nil
	}

	dataDisks := dataDisksRaw.([]interface{})

	for _, v := range dataDisks {
		disk := v.(map[string]interface{})
		dataDisk := compute.DataDisk{
			Lun:          utils.Int32(int32(disk["lun"].(int))),
			Caching:      compute.CachingTypes(disk["caching"].(string)),
			CreateOption: compute.DiskCreateOptionTypesEmpty,
			ManagedDisk: &compute.ManagedDiskParameters{
				StorageAccountType: compute.StorageAccountTypes(disk["storage_account_type"].(string)),
			},
		}

		name, ok := disk["name"].(string)
		if ok {
			dataDisk.Name = utils.String(name)
		}

		if diskSizeGb, ok := disk["disk_size_gb"].(int); ok {
			dataDisk.DiskSizeGB = utils.Int32(int32(diskSizeGb))
		}

		if managedDiskId, ok := disk["managed_disk_id"].(string); ok && managedDiskId != "" {
			dataDiskId, err := parse.ManagedDiskID(managedDiskId)
			if err != nil {
				return nil, fmt.Errorf("failed to parse Managed Disk ID for Data Disk %q", name)
			}

			existingDisk, err := disksClient.Get(ctx, dataDiskId.ResourceGroup, dataDiskId.Name)
			if err != nil {
				return nil, fmt.Errorf("failed to retrieve Managed Disk information for Data Disk %q (resource group %q)", dataDiskId.Name, dataDiskId.ResourceGroup)
			}

			dataDisk.ManagedDisk.ID = utils.String(managedDiskId)
			dataDisk.DiskSizeGB = existingDisk.DiskSizeGB

			// If this is the first pass through create option will be empty and the disk id must have been user specified so we set to `Attach`
			if createOption, ok := disk["create_option"].(string); ok && createOption == "" {
				dataDisk.CreateOption = compute.DiskCreateOptionTypesAttach
			}
		}

		if diskEncryptionSet, encryptionSetOk := disk["disk_encryption_set_id"].(string); encryptionSetOk && diskEncryptionSet != "" {
			dataDisk.ManagedDisk.DiskEncryptionSet = &compute.DiskEncryptionSetParameters{
				ID: utils.String(diskEncryptionSet),
			}
		}

		if writeAccelerator, ok := disk["write_accelerator_enabled"]; ok {
			dataDisk.WriteAcceleratorEnabled = utils.Bool(writeAccelerator.(bool))
		}

		result = append(result, dataDisk)
	}

	return &result, nil
}

func flattenVirtualMachineDataDisks(input *[]compute.DataDisk) []interface{} {
	if input == nil {
		return []interface{}{}
	}

	result := make([]interface{}, 0)
	for _, v := range *input {
		dataDisk := make(map[string]interface{})
		name := ""
		if v.Name != nil {
			name = *v.Name
		}
		dataDisk["name"] = name

		var lun int
		if v.Lun != nil {
			lun = int(*v.Lun)
		}
		dataDisk["lun"] = lun

		dataDisk["caching"] = string(v.Caching)
		storageAccountType := ""
		managedDiskID := ""
		diskEncryptionSetID := ""
		if v.ManagedDisk != nil {
			storageAccountType = string(v.ManagedDisk.StorageAccountType)
			if v.ManagedDisk.ID != nil {
				managedDiskID = *v.ManagedDisk.ID
			}
			if v.ManagedDisk.DiskEncryptionSet != nil && v.ManagedDisk.DiskEncryptionSet.ID != nil {
				diskEncryptionSetID = *v.ManagedDisk.DiskEncryptionSet.ID
			}
		}
		dataDisk["storage_account_type"] = storageAccountType
		dataDisk["managed_disk_id"] = managedDiskID
		dataDisk["disk_encryption_set_id"] = diskEncryptionSetID
		var diskSizeGB int
		if v.DiskSizeGB != nil {
			diskSizeGB = int(*v.DiskSizeGB)
		}
		dataDisk["disk_size_gb"] = diskSizeGB

		dataDisk["create_option"] = v.CreateOption

		writeAccelerator := false
		if v.WriteAcceleratorEnabled != nil {
			writeAccelerator = *v.WriteAcceleratorEnabled
		}
		dataDisk["write_accelerator_enabled"] = writeAccelerator

		diskIOPS := 0
		if v.DiskIOPSReadWrite != nil {
			diskIOPS = int(*v.DiskIOPSReadWrite)
		}
		dataDisk["disk_iops_read_write"] = diskIOPS

		diskMBPS := 0
		if v.DiskMBpsReadWrite != nil {
			diskMBPS = int(*v.DiskMBpsReadWrite)
		}
		dataDisk["disk_mbps_read_write"] = diskMBPS

		result = append(result, dataDisk)
	}

	return result
}
