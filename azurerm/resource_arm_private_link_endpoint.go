package azurerm

import (
	"fmt"
	"log"

	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2019-06-01/network"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/helpers/azure"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/helpers/response"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/helpers/tf"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/helpers/validate"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/features"
	aznet "github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/services/network"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/tags"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/utils"
)

func resourceArmPrivateLinkEndpoint() *schema.Resource {
	return &schema.Resource{
		Create: resourceArmPrivateLinkEndpointCreateUpdate,
		Read:   resourceArmPrivateLinkEndpointRead,
		Update: resourceArmPrivateLinkEndpointCreateUpdate,
		Delete: resourceArmPrivateLinkEndpointDelete,

		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		Schema: map[string]*schema.Schema{
			"name": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validate.NoEmptyStrings,
			},

			"location": azure.SchemaLocation(),

			"resource_group_name": azure.SchemaResourceGroupNameDiffSuppress(),

			"subnet_id": {
				Type:         schema.TypeString,
				Required:     true,
				ValidateFunc: validate.NoEmptyStrings,
			},

			"private_service_connection": {
				Type:     schema.TypeList,
				Optional: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"name": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validate.NoEmptyStrings,
						},
						"is_manual_connection": {
							Type:     schema.TypeBool,
							Required: true,
						},
						"private_connection_resource_id": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validate.NoEmptyStrings,
						},
						"subresource_names": {
							Type:     schema.TypeList,
							Optional: true,
							Elem: &schema.Schema{
								Type:         schema.TypeString,
								ValidateFunc: validate.NoEmptyStrings,
							},
						},
						"request_message": {
							Type:         schema.TypeString,
							Optional:     true,
							ValidateFunc: validate.PrivateLinkEnpointRequestMessage,
						},
						"provisioning_state": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"status": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"private_ip_address": {
							Type:     schema.TypeString,
							Computed: true,
						},
					},
				},
			},

			"network_interface_ids": {
				Type:     schema.TypeList,
				Computed: true,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},

			"tags": tags.Schema(),
		},
	}
}

func resourceArmPrivateLinkEndpointCreateUpdate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*ArmClient).Network.PrivateEndpointClient
	ctx := meta.(*ArmClient).StopContext

	name := d.Get("name").(string)
	resourceGroup := d.Get("resource_group_name").(string)

	if err := aznet.ValidatePrivateLinkEndpointSettings(d); err != nil {
		return fmt.Errorf("Error validating the configuration for the Private Link Endpoint %q (Resource Group %q): %+v", name, resourceGroup, err)
	}

	if features.ShouldResourcesBeImported() && d.IsNewResource() {
		resp, err := client.Get(ctx, resourceGroup, name, "")
		if err != nil {
			if !utils.ResponseWasNotFound(resp.Response) {
				return fmt.Errorf("Error checking for present of existing Private Link Endpoint %q (Resource Group %q): %+v", name, resourceGroup, err)
			}
		}
		if !utils.ResponseWasNotFound(resp.Response) {
			return tf.ImportAsExistsError("azurerm_private_link_endpoint", *resp.ID)
		}
	}

	location := azure.NormalizeLocation(d.Get("location").(string))
	privateServiceConnections := d.Get("private_service_connection").([]interface{})
	subnetId := d.Get("subnet_id").(string)
	t := d.Get("tags").(map[string]interface{})

	parameters := network.PrivateEndpoint{
		Location: utils.String(location),
		PrivateEndpointProperties: &network.PrivateEndpointProperties{
			PrivateLinkServiceConnections:       expandArmPrivateLinkEndpointServiceConnection(privateServiceConnections, false),
			ManualPrivateLinkServiceConnections: expandArmPrivateLinkEndpointServiceConnection(privateServiceConnections, true),
			Subnet: &network.Subnet{
				ID: utils.String(subnetId),
			},
		},
		Tags: tags.Expand(t),
	}

	future, err := client.CreateOrUpdate(ctx, resourceGroup, name, parameters)
	if err != nil {
		return fmt.Errorf("Error creating Private Link Endpoint %q (Resource Group %q): %+v", name, resourceGroup, err)
	}
	if err = future.WaitForCompletionRef(ctx, client.Client); err != nil {
		return fmt.Errorf("Error waiting for creation of Private Link Endpoint %q (Resource Group %q): %+v", name, resourceGroup, err)
	}

	resp, err := client.Get(ctx, resourceGroup, name, "")
	if err != nil {
		return fmt.Errorf("Error retrieving Private Link Endpoint %q (Resource Group %q): %+v", name, resourceGroup, err)
	}
	if resp.ID == nil || *resp.ID == "" {
		return fmt.Errorf("API returns a nil/empty id on Private Link Endpoint %q (Resource Group %q): %+v", name, resourceGroup, err)
	}
	d.SetId(*resp.ID)

	return resourceArmPrivateLinkEndpointRead(d, meta)
}

func resourceArmPrivateLinkEndpointRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*ArmClient).Network.PrivateEndpointClient
	ctx := meta.(*ArmClient).StopContext

	id, err := azure.ParseAzureResourceID(d.Id())
	if err != nil {
		return err
	}
	resourceGroup := id.ResourceGroup
	name := id.Path["privateEndpoints"]

	resp, err := client.Get(ctx, resourceGroup, name, "")
	if err != nil {
		if utils.ResponseWasNotFound(resp.Response) {
			log.Printf("[INFO] Private Link Endpoint %q does not exist - removing from state", d.Id())
			d.SetId("")
			return nil
		}
		return fmt.Errorf("Error reading Private Link Endpoint %q (Resource Group %q): %+v", name, resourceGroup, err)
	}

	d.Set("name", resp.Name)
	d.Set("resource_group_name", resourceGroup)
	if location := resp.Location; location != nil {
		d.Set("location", azure.NormalizeLocation(*location))
	}
	if props := resp.PrivateEndpointProperties; props != nil {
		if subnet := props.Subnet; subnet != nil {
			d.Set("subnet_id", subnet.ID)
		}

		privateIpAddress := ""

		if props.NetworkInterfaces != nil {
			if err := d.Set("network_interface_ids", flattenArmPrivateLinkEndpointInterface(props.NetworkInterfaces)); err != nil {
				return fmt.Errorf("Error setting `network_interface_ids`: %+v", err)
			}

			// now we need to get the nic to get the private ip address for the private link endpoint
			client := meta.(*ArmClient).Network.InterfacesClient
			ctx := meta.(*ArmClient).StopContext

			nic := d.Get("network_interface_ids").([]interface{})

			nicId, err := azure.ParseAzureResourceID(nic[0].(string))
			if err != nil {
				return err
			}
			nicName := nicId.Path["networkInterfaces"]

			nicResp, err := client.Get(ctx, resourceGroup, nicName, "")
			if err != nil {
				if utils.ResponseWasNotFound(nicResp.Response) {
					return fmt.Errorf("Azure Network Interface %q (Resource Group %q): %+v", nicName, resourceGroup, err)
				}
				return fmt.Errorf("Error making Read request on Azure Network Interface %q (Resource Group %q): %+v", nicName, resourceGroup, err)
			}

			if nicProps := nicResp.InterfacePropertiesFormat; nicProps != nil {
				if configs := nicProps.IPConfigurations; configs != nil {
					for i, config := range *nicProps.IPConfigurations {
						if ipProps := config.InterfaceIPConfigurationPropertiesFormat; ipProps != nil {
							if v := ipProps.PrivateIPAddress; v != nil {
								if i == 0 {
									privateIpAddress = *v
								}
							}
						}
					}
				}
			}
		}

		if err := d.Set("private_service_connection", flattenArmPrivateLinkEndpointServiceConnection(props.PrivateLinkServiceConnections, props.ManualPrivateLinkServiceConnections, privateIpAddress)); err != nil {
			return fmt.Errorf("Error setting `private_service_connection`: %+v", err)
		}
	}

	return tags.FlattenAndSet(d, resp.Tags)
}

func resourceArmPrivateLinkEndpointDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*ArmClient).Network.PrivateEndpointClient
	ctx := meta.(*ArmClient).StopContext

	id, err := azure.ParseAzureResourceID(d.Id())
	if err != nil {
		return err
	}
	resourceGroup := id.ResourceGroup
	name := id.Path["privateEndpoints"]

	future, err := client.Delete(ctx, resourceGroup, name)
	if err != nil {
		if response.WasNotFound(future.Response()) {
			return nil
		}
		return fmt.Errorf("Error deleting Private Link Endpoint %q (Resource Group %q): %+v", name, resourceGroup, err)
	}

	if err = future.WaitForCompletionRef(ctx, client.Client); err != nil {
		if !response.WasNotFound(future.Response()) {
			return fmt.Errorf("Error waiting for deleting Private Link Endpoint %q (Resource Group %q): %+v", name, resourceGroup, err)
		}
	}

	return nil
}

func expandArmPrivateLinkEndpointServiceConnection(input []interface{}, parseManual bool) *[]network.PrivateLinkServiceConnection {
	results := make([]network.PrivateLinkServiceConnection, 0)
	for _, item := range input {
		v := item.(map[string]interface{})
		privateConnectonResourceId := v["private_connection_resource_id"].(string)
		subresourceNames := v["subresource_names"].([]interface{})
		requestMessage := v["request_message"].(string)
		isManual := v["is_manual_connection"].(bool)
		name := v["name"].(string)

		if isManual == parseManual {
			result := network.PrivateLinkServiceConnection{
				Name: utils.String(name),
				PrivateLinkServiceConnectionProperties: &network.PrivateLinkServiceConnectionProperties{
					GroupIds:             utils.ExpandStringSlice(subresourceNames),
					PrivateLinkServiceID: utils.String(privateConnectonResourceId),
				},
			}

			if requestMessage != "" {
				result.PrivateLinkServiceConnectionProperties.RequestMessage = utils.String(requestMessage)
			}

			results = append(results, result)
		}
	}

	return &results
}

func flattenArmPrivateLinkEndpointServiceConnection(serviceConnections *[]network.PrivateLinkServiceConnection, manualServiceConnections *[]network.PrivateLinkServiceConnection, privateIpAddress string) []interface{} {
	results := make([]interface{}, 0)
	if serviceConnections == nil && manualServiceConnections == nil {
		return results
	}

	if serviceConnections != nil {
		for _, item := range *serviceConnections {
			v := make(map[string]interface{})

			if name := item.Name; name != nil {
				v["name"] = *name
			}

			v["is_manual_connection"] = false
			v["private_ip_address"] = privateIpAddress

			if props := item.PrivateLinkServiceConnectionProperties; props != nil {
				if subresourceNames := props.GroupIds; subresourceNames != nil {
					v["subresource_names"] = utils.FlattenStringSlice(subresourceNames)
				}
				if privateConnectionResourceId := props.PrivateLinkServiceID; privateConnectionResourceId != nil {
					v["private_connection_resource_id"] = *privateConnectionResourceId
				}
				if requestMessage := props.RequestMessage; requestMessage != nil {
					v["request_message"] = *requestMessage
				}
				if provisioningState := props.ProvisioningState; provisioningState != "" {
					v["provisioning_state"] = provisioningState
				}

				if s := props.PrivateLinkServiceConnectionState; s != nil {
					if status := s.Status; status != nil {
						v["status"] = *status
					}
				}
			}

			results = append(results, v)
		}
	}

	if manualServiceConnections != nil {
		for _, item := range *manualServiceConnections {
			v := make(map[string]interface{})

			if name := item.Name; name != nil {
				v["name"] = *name
			}

			v["is_manual_connection"] = true
			v["private_ip_address"] = privateIpAddress

			if props := item.PrivateLinkServiceConnectionProperties; props != nil {
				if subresourceNames := props.GroupIds; subresourceNames != nil {
					v["subresource_names"] = utils.FlattenStringSlice(subresourceNames)
				}
				if privateConnectionResourceId := props.PrivateLinkServiceID; privateConnectionResourceId != nil {
					v["private_connection_resource_id"] = *privateConnectionResourceId
				}
				if requestMessage := props.RequestMessage; requestMessage != nil {
					v["request_message"] = *requestMessage
				}
				if provisioningState := props.ProvisioningState; provisioningState != "" {
					v["provisioning_state"] = provisioningState
				}
				if s := props.PrivateLinkServiceConnectionState; s != nil {
					if status := s.Status; status != nil {
						v["status"] = *status
					}
				}
			}

			results = append(results, v)
		}
	}

	return results
}

func flattenArmPrivateLinkEndpointInterface(input *[]network.Interface) []string {
	if input == nil {
		return make([]string, 0)
	}

	results := make([]string, 0)

	for _, item := range *input {
		if id := item.ID; id != nil {
			results = append(results, *id)
		}
	}

	return results
}
