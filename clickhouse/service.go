package clickhouse

import (
	"context"
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure the implementation satisfies the expected interfaces.
var (
	_ resource.Resource                = &serviceResource{}
	_ resource.ResourceWithConfigure   = &serviceResource{}
	_ resource.ResourceWithImportState = &serviceResource{}
)

// NewServiceResource is a helper function to simplify the provider implementation.
func NewServiceResource() resource.Resource {
	return &serviceResource{}
}

// serviceResource is the resource implementation.
type serviceResource struct {
	client *Client
}

type serviceResourceModel struct {
	ID                 types.String    `tfsdk:"id"`
	Name               types.String    `tfsdk:"name"`
	Password           types.String    `tfsdk:"password"`
	Endpoints          types.List      `tfsdk:"endpoints"`
	CloudProvider      types.String    `tfsdk:"cloud_provider"`
	Region             types.String    `tfsdk:"region"`
	Tier               types.String    `tfsdk:"tier"`
	IdleScaling        types.Bool      `tfsdk:"idle_scaling"`
	IpAccessList       []IpAccessModel `tfsdk:"ip_access"`
	MinTotalMemoryGb   types.Int64     `tfsdk:"min_total_memory_gb"`
	MaxTotalMemoryGb   types.Int64     `tfsdk:"max_total_memory_gb"`
	IdleTimeoutMinutes types.Int64     `tfsdk:"idle_timeout_minutes"`
	LastUpdated        types.String    `tfsdk:"last_updated"`
}

var endpointObjectType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"protocol": types.StringType,
		"host":     types.StringType,
		"port":     types.Int64Type,
	},
}

type IpAccessModel struct {
	Source      types.String `tfsdk:"source"`
	Description types.String `tfsdk:"description"`
}

// Metadata returns the resource type name.
func (r *serviceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service"
}

// Schema defines the schema for the resource.
func (r *serviceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "ID of the created service. Generated by ClickHouse Cloud.",
				Computed:    true,
			},
			"last_updated": schema.StringAttribute{
				Description: "Date for when the service was last updated by Terraform.",
				Computed:    true,
			},
			"name": schema.StringAttribute{
				Description: "User defined identifier for the service.",
				Required:    true,
			},
			"password": schema.StringAttribute{
				Description: "Password for a default user. If not provided, a random password will be generated.",
				Required:    false,
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
			},
			"cloud_provider": schema.StringAttribute{
				Description: "Cloud provider ('aws' or 'gcp') in which the service is deployed in.",
				Required:    true,
			},
			"region": schema.StringAttribute{
				Description: "Region within the cloud provider in which the service is deployed in.",
				Required:    true,
			},
			"tier": schema.StringAttribute{
				Description: "Tier of the service: 'development', 'production'. Production services scale, Development are fixed size.",
				Required:    true,
			},
			"idle_scaling": schema.BoolAttribute{
				Description: "When set to true the service is allowed to scale down to zero when idle. Always true for development services.",
				Required:    true,
			},
			"ip_access": schema.ListNestedAttribute{
				Description: "List of IP addresses allowed to access the service.",
				Required:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"source": schema.StringAttribute{
							Description: "IP address allowed to access the service.",
							Required:    true,
						},
						"description": schema.StringAttribute{
							Description: "Description of the IP address.",
							Optional:    true,
						},
					},
				},
			},
			"endpoints": schema.ListNestedAttribute{
				Description: "List of public endpoints.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"protocol": schema.StringAttribute{
							Description: "Endpoint protocol: https or nativesecure",
							Computed:    true,
						},
						"host": schema.StringAttribute{
							Description: "Endpoint host.",
							Computed:    true,
						},
						"port": schema.Int64Attribute{
							Description: "Endpoint port.",
							Computed:    true,
						},
					},
				},
			},
			"min_total_memory_gb": schema.Int64Attribute{
				Description: "Minimum total memory of all workers during auto-scaling in Gb. Available only for 'production' services. Must be a multiple of 12 and greater than 24.",
				Required:    true,
			},
			"max_total_memory_gb": schema.Int64Attribute{
				Description: "Maximum total memory of all workers during auto-scaling in Gb. Available only for 'production' services. Must be a multiple of 12 and lower than 360 for non paid services or 720 for paid services.",
				Required:    true,
			},
			"idle_timeout_minutes": schema.Int64Attribute{
				Description: "Set minimum idling timeout (in minutes). Must be greater than or equal to 5 minutes.",
				Required:    true,
			},
		},
	}
}

// Configure adds the provider configured client to the resource.
func (r *serviceResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	r.client = req.ProviderData.(*Client)
}

// Create a new resource
func (r *serviceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// Retrieve values from plan
	var plan serviceResourceModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Generate API request body from plan
	service := Service{
		Name:               string(plan.Name.ValueString()),
		Provider:           string(plan.CloudProvider.ValueString()),
		Region:             string(plan.Region.ValueString()),
		Tier:               string(plan.Tier.ValueString()),
		IdleScaling:        bool(plan.IdleScaling.ValueBool()),
		MinTotalMemoryGb:   int(plan.MinTotalMemoryGb.ValueInt64()),
		MaxTotalMemoryGb:   int(plan.MaxTotalMemoryGb.ValueInt64()),
		IdleTimeoutMinutes: int(plan.IdleTimeoutMinutes.ValueInt64()),
	}
	for _, item := range plan.IpAccessList {
		service.IpAccessList = append(service.IpAccessList, IpAccess{
			Source:      string(item.Source.ValueString()),
			Description: string(item.Description.ValueString()),
		})
	}

	// Create new service
	s, password, err := r.client.CreateService(service)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error creating service",
			"Could not create service, unexpected error: "+err.Error(),
		)
		return
	}

	for {
		s, err = r.client.GetService(s.Id)
		if err != nil {
			resp.Diagnostics.AddError(
				"Error retrieving service state",
				"Could not retrieve service state after creation, unexpected error: "+err.Error(),
			)
			return
		}

		if s.State != "provisioning" {
			break
		}

		time.Sleep(time.Second * 5)
	}

	// Update service password if provided explicitly
	if password = plan.Password.ValueString(); len(password) > 0 {
		_, err := r.client.UpdateServicePassword(s.Id, ServicePasswordUpdateFromPlainPassword(password))
		if err != nil {
			resp.Diagnostics.AddError(
				"Error setting service password",
				"Could not set service password after creation, unexpected error: "+err.Error(),
			)
			return
		}
	}

	// Map response body to schema and populate Computed attribute values
	plan.ID = types.StringValue(s.Id)
	plan.Name = types.StringValue(s.Name)
	plan.Password = types.StringValue(password)
	plan.CloudProvider = types.StringValue(s.Provider)
	plan.Region = types.StringValue(s.Region)
	plan.Tier = types.StringValue(s.Tier)
	plan.IdleScaling = types.BoolValue(s.IdleScaling)
	plan.MinTotalMemoryGb = types.Int64Value(int64(s.MinTotalMemoryGb))
	plan.MaxTotalMemoryGb = types.Int64Value(int64(s.MaxTotalMemoryGb))
	plan.IdleTimeoutMinutes = types.Int64Value(int64(s.IdleTimeoutMinutes))
	for ipAccessIndex, ipAccess := range s.IpAccessList {
		plan.IpAccessList[ipAccessIndex] = IpAccessModel{
			Source:      types.StringValue(ipAccess.Source),
			Description: types.StringValue(ipAccess.Description),
		}
	}

	var values []attr.Value
	for _, endpoint := range service.Endpoints {
		obj, _ := types.ObjectValue(endpointObjectType.AttrTypes, map[string]attr.Value{
			"protocol": types.StringValue(endpoint.Protocol),
			"host":     types.StringValue(endpoint.Host),
			"port":     types.Int64Value(int64(endpoint.Port)),
		})

		values = append(values, obj)
	}

	plan.Endpoints, _ = types.ListValue(endpointObjectType, values)

	plan.LastUpdated = types.StringValue(time.Now().Format(time.RFC850))

	// Set state to fully populated data
	diags = resp.State.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
}

// Read refreshes the Terraform state with the latest data.
func (r *serviceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serviceResourceModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Get refreshed service value from ClickHouse OpenAPI
	service, err := r.client.GetService(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading ClickHouse Service",
			"Could not read ClickHouse service id "+state.ID.ValueString()+": "+err.Error(),
		)
		return
	}

	// Overwrite items with refreshed state
	state.IpAccessList = []IpAccessModel{}
	for _, item := range service.IpAccessList {
		state.IpAccessList = append(state.IpAccessList, IpAccessModel{
			Source:      types.StringValue(item.Source),
			Description: types.StringValue(item.Description),
		})
	}

	var values []attr.Value
	for _, endpoint := range service.Endpoints {
		obj, _ := types.ObjectValue(endpointObjectType.AttrTypes, map[string]attr.Value{
			"protocol": types.StringValue(endpoint.Protocol),
			"host":     types.StringValue(endpoint.Host),
			"port":     types.Int64Value(int64(endpoint.Port)),
		})

		values = append(values, obj)
	}
	state.Endpoints, _ = types.ListValue(endpointObjectType, values)

	// Set refreshed state
	diags = resp.State.Set(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
}

// Update updates the resource and sets the updated Terraform state on success.
func (r *serviceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Retrieve values from plan
	var plan, state serviceResourceModel
	diags := req.Plan.Get(ctx, &plan)
	diags = req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)

	if plan.CloudProvider != state.CloudProvider {
		resp.Diagnostics.AddAttributeError(
			path.Root("cloud_provider"),
			"Invalid Update",
			"ClickHouse does not support changing service cloud providers",
		)
	}

	if plan.Region != state.Region {
		resp.Diagnostics.AddAttributeError(
			path.Root("region"),
			"Invalid Update",
			"ClickHouse does not support changing service regions",
		)
	}

	if plan.Tier != state.Tier {
		resp.Diagnostics.AddAttributeError(
			path.Root("tier"),
			"Invalid Update",
			"ClickHouse does not support changing service tiers",
		)
	}

	if resp.Diagnostics.HasError() {
		return
	}

	// Generate API request body from plan
	serviceId := state.ID.ValueString()
	service := ServiceUpdate{
		Name:         "",
		IpAccessList: nil,
	}
	serviceChange := false

	if plan.Name != state.Name {
		service.Name = plan.Name.ValueString()
		serviceChange = true
	}
	if !equal(plan.IpAccessList, state.IpAccessList) {
		serviceChange = true
		ipAccessListRawOld := state.IpAccessList
		ipAccessListRawNew := plan.IpAccessList

		ipAccessListOld := []IpAccess{}
		ipAccessListNew := []IpAccess{}

		for _, item := range ipAccessListRawOld {
			ipAccess := IpAccess{
				Source:      item.Source.ValueString(),
				Description: item.Description.ValueString(),
			}

			ipAccessListOld = append(ipAccessListOld, ipAccess)
		}

		for _, item := range ipAccessListRawNew {
			ipAccess := IpAccess{
				Source:      item.Source.ValueString(),
				Description: item.Description.ValueString(),
			}

			ipAccessListNew = append(ipAccessListNew, ipAccess)
		}

		add, remove := diffArrays(ipAccessListOld, ipAccessListNew, func(a IpAccess) string {
			return fmt.Sprintf("%s:%s", a.Source, a.Description)
		})

		service.IpAccessList = &IpAccessUpdate{
			Add:    add,
			Remove: remove,
		}
	}

	// Update existing order
	var s *Service
	if serviceChange {
		var err error
		s, err = r.client.UpdateService(serviceId, service)
		if err != nil {
			resp.Diagnostics.AddError(
				"Error Updating ClickHouse Service",
				"Could not update service, unexpected error: "+err.Error(),
			)
			return
		}
	}

	scalingChange := false
	serviceScaling := ServiceScalingUpdate{
		IdleScaling: state.IdleScaling.ValueBoolPointer(),
	}

	if plan.IdleScaling != state.IdleScaling {
		scalingChange = true
		idleScaling := new(bool)
		*idleScaling = plan.IdleScaling.ValueBool()
		serviceScaling.IdleScaling = idleScaling
	}
	if plan.MinTotalMemoryGb != state.MinTotalMemoryGb {
		scalingChange = true
		serviceScaling.MinTotalMemoryGb = int(plan.MinTotalMemoryGb.ValueInt64())
	}
	if plan.MaxTotalMemoryGb != state.MaxTotalMemoryGb {
		scalingChange = true
		serviceScaling.MaxTotalMemoryGb = int(plan.MaxTotalMemoryGb.ValueInt64())
	}
	if plan.IdleTimeoutMinutes != state.IdleTimeoutMinutes {
		scalingChange = true
		serviceScaling.IdleTimeoutMinutes = int(plan.IdleTimeoutMinutes.ValueInt64())
	}

	if scalingChange {
		var err error
		s, err = r.client.UpdateServiceScaling(serviceId, serviceScaling)
		if err != nil {
			resp.Diagnostics.AddError(
				"Error Updating ClickHouse Service Scaling",
				"Could not update service scaling, unexpected error: "+err.Error(),
			)
			return
		}
	}

	password := state.Password.String()
	if plan.Password != state.Password {
		password = plan.Password.ValueString()
		res, err := r.client.UpdateServicePassword(serviceId, ServicePasswordUpdateFromPlainPassword(password))
		if err != nil {
			resp.Diagnostics.AddError(
				"Error Updating ClickHouse Service Password",
				"Could not update service password, unexpected error: "+err.Error(),
			)
			return
		}

		// empty password provided, ClickHouse Cloud return a new generated password
		if len(res.Password) > 0 {
			password = res.Password
		}
	}

	// Update resource state with updated items and timestamp
	plan.ID = types.StringValue(s.Id)
	plan.Name = types.StringValue(s.Name)
	plan.Password = types.StringValue(password)
	plan.CloudProvider = types.StringValue(s.Provider)
	plan.Region = types.StringValue(s.Region)
	plan.Tier = types.StringValue(s.Tier)
	plan.IdleScaling = types.BoolValue(s.IdleScaling)
	plan.MinTotalMemoryGb = types.Int64Value(int64(s.MinTotalMemoryGb))
	plan.MaxTotalMemoryGb = types.Int64Value(int64(s.MaxTotalMemoryGb))
	plan.IdleTimeoutMinutes = types.Int64Value(int64(s.IdleTimeoutMinutes))
	for ipAccessIndex, ipAccess := range s.IpAccessList {
		plan.IpAccessList[ipAccessIndex] = IpAccessModel{
			Source:      types.StringValue(ipAccess.Source),
			Description: types.StringValue(ipAccess.Description),
		}
	}
	plan.LastUpdated = types.StringValue(time.Now().Format(time.RFC850))

	diags = resp.State.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
}

// Delete deletes the resource and removes the Terraform state on success.
func (r *serviceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// Retrieve values from state
	var state serviceResourceModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Delete existing order
	_, err := r.client.DeleteService(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Deleting ClickHouse Service",
			"Could not delete service, unexpected error: "+err.Error(),
		)
		return
	}
}

func (r *serviceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Retrieve import ID and save to id attribute
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
