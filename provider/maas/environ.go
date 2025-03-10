// Copyright 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package maas

import (
	stdcontext "context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juju/collections/set"
	"github.com/juju/errors"
	"github.com/juju/gomaasapi/v2"
	"github.com/juju/names/v4"
	"github.com/juju/utils/v2"
	"github.com/juju/version/v2"

	"github.com/juju/juju/cloudconfig/cloudinit"
	"github.com/juju/juju/cloudconfig/instancecfg"
	"github.com/juju/juju/cloudconfig/providerinit"
	"github.com/juju/juju/core/constraints"
	"github.com/juju/juju/core/instance"
	corenetwork "github.com/juju/juju/core/network"
	"github.com/juju/juju/core/os"
	"github.com/juju/juju/core/series"
	"github.com/juju/juju/core/status"
	"github.com/juju/juju/environs"
	environscloudspec "github.com/juju/juju/environs/cloudspec"
	"github.com/juju/juju/environs/config"
	"github.com/juju/juju/environs/context"
	"github.com/juju/juju/environs/instances"
	"github.com/juju/juju/environs/storage"
	"github.com/juju/juju/environs/tags"
	"github.com/juju/juju/network"
	"github.com/juju/juju/provider/common"
	"github.com/juju/juju/tools"
)

const (
	// The version strings indicating the MAAS API version.
	apiVersion2 = "2.0"
)

// A request may fail due to "eventual consistency" semantics, which
// should resolve fairly quickly.  A request may also fail due to a slow
// state transition (for instance an instance taking a while to release
// a security group after termination).  The former failure mode is
// dealt with by shortAttempt, the latter by LongAttempt.
var shortAttempt = utils.AttemptStrategy{
	Total: 5 * time.Second,
	Delay: 200 * time.Millisecond,
}

var (
	DeploymentStatusCall = deploymentStatusCall
	GetMAAS2Controller   = getMAAS2Controller
)

func getMAAS2Controller(maasServer, apiKey string) (gomaasapi.Controller, error) {
	return gomaasapi.NewController(gomaasapi.ControllerArgs{
		BaseURL: maasServer,
		APIKey:  apiKey,
	})
}

type maasEnviron struct {
	name string
	uuid string

	// archMutex gates access to supportedArchitectures
	archMutex sync.Mutex

	// ecfgMutex protects the *Unlocked fields below.
	ecfgMutex sync.Mutex

	ecfgUnlocked       *maasModelConfig
	maasClientUnlocked *gomaasapi.MAASObject
	storageUnlocked    storage.Storage

	// maasController provides access to the MAAS 2.0 API.
	maasController gomaasapi.Controller

	// namespace is used to create the machine and device hostnames.
	namespace instance.Namespace

	availabilityZonesMutex sync.Mutex
	availabilityZones      corenetwork.AvailabilityZones

	// apiVersion tells us if we are using the MAAS 1.0 or 2.0 api.
	apiVersion string

	// GetCapabilities is a function that connects to MAAS to return its set of
	// capabilities.
	GetCapabilities Capabilities
}

var _ environs.Environ = (*maasEnviron)(nil)
var _ environs.Networking = (*maasEnviron)(nil)

// Capabilities is an alias for a function that gets
// the capabilities of a MAAS installation.
type Capabilities = func(client *gomaasapi.MAASObject, serverURL string) (set.Strings, error)

func NewEnviron(cloud environscloudspec.CloudSpec, cfg *config.Config, getCaps Capabilities) (*maasEnviron, error) {
	if getCaps == nil {
		getCaps = getCapabilities
	}
	env := &maasEnviron{
		name:            cfg.Name(),
		uuid:            cfg.UUID(),
		GetCapabilities: getCaps,
	}
	if err := env.SetConfig(cfg); err != nil {
		return nil, errors.Trace(err)
	}
	if err := env.SetCloudSpec(stdcontext.TODO(), cloud); err != nil {
		return nil, errors.Trace(err)
	}

	var err error
	env.namespace, err = instance.NewNamespace(cfg.UUID())
	if err != nil {
		return nil, errors.Trace(err)
	}
	return env, nil
}

// PrepareForBootstrap is part of the Environ interface.
func (env *maasEnviron) PrepareForBootstrap(_ environs.BootstrapContext, _ string) error {
	return nil
}

// Create is part of the Environ interface.
func (env *maasEnviron) Create(_ context.ProviderCallContext, _ environs.CreateParams) error {
	return nil
}

// Bootstrap is part of the Environ interface.
func (env *maasEnviron) Bootstrap(
	ctx environs.BootstrapContext, callCtx context.ProviderCallContext, args environs.BootstrapParams,
) (*environs.BootstrapResult, error) {
	result, series, finalizer, err := common.BootstrapInstance(ctx, env, callCtx, args)
	if err != nil {
		return nil, err
	}

	// We want to destroy the started instance if it doesn't transition to Deployed.
	defer func() {
		if err != nil {
			if err := env.StopInstances(callCtx, result.Instance.Id()); err != nil {
				logger.Errorf("error releasing bootstrap instance: %v", err)
			}
		}
	}()

	waitingFinalizer := func(
		ctx environs.BootstrapContext,
		icfg *instancecfg.InstanceConfig,
		dialOpts environs.BootstrapDialOpts,
	) error {
		// Wait for bootstrap instance to change to deployed state.
		if err := env.waitForNodeDeployment(callCtx, result.Instance.Id(), dialOpts.Timeout); err != nil {
			return errors.Annotate(err, "bootstrap instance started but did not change to Deployed state")
		}
		return finalizer(ctx, icfg, dialOpts)
	}

	bsResult := &environs.BootstrapResult{
		Arch:                    *result.Hardware.Arch,
		Series:                  series,
		CloudBootstrapFinalizer: waitingFinalizer,
	}
	return bsResult, nil
}

// ControllerInstances is specified in the Environ interface.
func (env *maasEnviron) ControllerInstances(ctx context.ProviderCallContext, controllerUUID string) ([]instance.Id, error) {
	instances, err := env.instances(ctx, gomaasapi.MachinesArgs{
		OwnerData: map[string]string{
			tags.JujuIsController: "true",
			tags.JujuController:   controllerUUID,
		},
	})
	if err != nil {
		return nil, errors.Trace(err)
	}
	if len(instances) == 0 {
		return nil, environs.ErrNotBootstrapped
	}
	ids := make([]instance.Id, len(instances))
	for i := range instances {
		ids[i] = instances[i].Id()
	}
	return ids, nil
}

// ecfg returns the environment's maasModelConfig, and protects it with a
// mutex.
func (env *maasEnviron) ecfg() *maasModelConfig {
	env.ecfgMutex.Lock()
	cfg := *env.ecfgUnlocked
	env.ecfgMutex.Unlock()
	return &cfg
}

// Config is specified in the Environ interface.
func (env *maasEnviron) Config() *config.Config {
	return env.ecfg().Config
}

// SetConfig is specified in the Environ interface.
func (env *maasEnviron) SetConfig(cfg *config.Config) error {
	env.ecfgMutex.Lock()
	defer env.ecfgMutex.Unlock()

	// The new config has already been validated by itself, but now we
	// validate the transition from the old config to the new.
	var oldCfg *config.Config
	if env.ecfgUnlocked != nil {
		oldCfg = env.ecfgUnlocked.Config
	}
	cfg, err := env.Provider().Validate(cfg, oldCfg)
	if err != nil {
		return errors.Trace(err)
	}

	ecfg, err := providerInstance.newConfig(cfg)
	if err != nil {
		return errors.Trace(err)
	}

	env.ecfgUnlocked = ecfg

	return nil
}

// SetCloudSpec is specified in the environs.Environ interface.
func (env *maasEnviron) SetCloudSpec(_ stdcontext.Context, spec environscloudspec.CloudSpec) error {
	env.ecfgMutex.Lock()
	defer env.ecfgMutex.Unlock()

	maasServer, err := parseCloudEndpoint(spec.Endpoint)
	if err != nil {
		return errors.Trace(err)
	}
	maasOAuth, err := parseOAuthToken(*spec.Credential)
	if err != nil {
		return errors.Trace(err)
	}

	apiVersion := apiVersion2
	controller, err := GetMAAS2Controller(maasServer, maasOAuth)
	if err != nil {
		return errors.Trace(err)
	}

	env.maasController = controller
	env.apiVersion = apiVersion
	env.storageUnlocked = NewStorage(env)

	return nil
}

func (env *maasEnviron) getSupportedArchitectures(ctx context.ProviderCallContext) ([]string, error) {
	env.archMutex.Lock()
	defer env.archMutex.Unlock()

	resources, err := env.maasController.BootResources()
	if err != nil {
		common.HandleCredentialError(IsAuthorisationFailure, err, ctx)
		return nil, errors.Trace(err)
	}
	architectures := set.NewStrings()
	for _, resource := range resources {
		architectures.Add(strings.Split(resource.Architecture(), "/")[0])
	}
	return architectures.SortedValues(), nil
}

// SupportsSpaces is specified on environs.Networking.
func (env *maasEnviron) SupportsSpaces(ctx context.ProviderCallContext) (bool, error) {
	return true, nil
}

// SupportsSpaceDiscovery is specified on environs.Networking.
func (env *maasEnviron) SupportsSpaceDiscovery(ctx context.ProviderCallContext) (bool, error) {
	return true, nil
}

// SupportsContainerAddresses is specified on environs.Networking.
func (env *maasEnviron) SupportsContainerAddresses(ctx context.ProviderCallContext) (bool, error) {
	return true, nil
}

type maasAvailabilityZone struct {
	name string
}

func (z maasAvailabilityZone) Name() string {
	return z.name
}

func (z maasAvailabilityZone) Available() bool {
	// MAAS' physical zone attributes only include name and description;
	// there is no concept of availability.
	return true
}

// AvailabilityZones returns a slice of availability zones
// for the configured region.
func (env *maasEnviron) AvailabilityZones(ctx context.ProviderCallContext) (corenetwork.AvailabilityZones, error) {
	env.availabilityZonesMutex.Lock()
	defer env.availabilityZonesMutex.Unlock()
	if env.availabilityZones == nil {
		availabilityZones, err := env.availabilityZones2(ctx)
		if err != nil {
			return nil, errors.Trace(err)
		}
		env.availabilityZones = availabilityZones
	}
	return env.availabilityZones, nil
}

func (env *maasEnviron) availabilityZones2(ctx context.ProviderCallContext) (corenetwork.AvailabilityZones, error) {
	zones, err := env.maasController.Zones()
	if err != nil {
		common.HandleCredentialError(IsAuthorisationFailure, err, ctx)
		return nil, errors.Trace(err)
	}
	availabilityZones := make(corenetwork.AvailabilityZones, len(zones))
	for i, zone := range zones {
		availabilityZones[i] = maasAvailabilityZone{zone.Name()}
	}
	return availabilityZones, nil
}

// InstanceAvailabilityZoneNames returns the availability zone names for each
// of the specified instances.
func (env *maasEnviron) InstanceAvailabilityZoneNames(ctx context.ProviderCallContext, ids []instance.Id) (map[instance.Id]string, error) {
	instances, err := env.Instances(ctx, ids)
	if err != nil && err != environs.ErrPartialInstances {
		return nil, err
	}
	zones := make(map[instance.Id]string, 0)
	for _, inst := range instances {
		if inst == nil {
			continue
		}
		mInst, ok := inst.(maasInstance)
		if !ok {
			continue
		}
		z, err := mInst.zone()
		if err != nil {
			logger.Errorf("could not get availability zone %v", err)
			continue
		}
		zones[inst.Id()] = z
	}
	return zones, nil
}

// DeriveAvailabilityZones is part of the common.ZonedEnviron interface.
func (env *maasEnviron) DeriveAvailabilityZones(ctx context.ProviderCallContext, args environs.StartInstanceParams) ([]string, error) {
	if args.Placement != "" {
		placement, err := env.parsePlacement(ctx, args.Placement)
		if err != nil {
			return nil, errors.Trace(err)
		}
		if placement.zoneName != "" {
			return []string{placement.zoneName}, nil
		}
	}
	return nil, nil
}

type maasPlacement struct {
	nodeName string
	zoneName string
	systemId string
}

func (env *maasEnviron) parsePlacement(ctx context.ProviderCallContext, placement string) (*maasPlacement, error) {
	pos := strings.IndexRune(placement, '=')
	if pos == -1 {
		// If there's no '=' delimiter, assume it's a node name.
		return &maasPlacement{nodeName: placement}, nil
	}
	switch key, value := placement[:pos], placement[pos+1:]; key {
	case "zone":
		zones, err := env.AvailabilityZones(ctx)
		if err != nil {
			return nil, errors.Trace(err)
		}
		if err := zones.Validate(value); err != nil {
			return nil, errors.Trace(err)
		}

		return &maasPlacement{zoneName: value}, nil
	case "system-id":
		return &maasPlacement{systemId: value}, nil
	}

	return nil, errors.Errorf("unknown placement directive: %v", placement)
}

func (env *maasEnviron) PrecheckInstance(ctx context.ProviderCallContext, args environs.PrecheckInstanceParams) error {
	if args.Placement == "" {
		return nil
	}
	_, err := env.parsePlacement(ctx, args.Placement)
	return err
}

// getCapabilities asks the MAAS server for its capabilities, if
// supported by the server.
func getCapabilities(client *gomaasapi.MAASObject, serverURL string) (set.Strings, error) {
	caps := make(set.Strings)
	var result gomaasapi.JSONObject
	var err error

	for a := shortAttempt.Start(); a.Next(); {
		ver := client.GetSubObject("version/")
		result, err = ver.CallGet("", nil)
		if err == nil {
			break
		}
		if err, ok := errors.Cause(err).(gomaasapi.ServerError); ok && err.StatusCode == 404 {
			logger.Debugf("Failed attempting to get capabilities from maas endpoint %q: %v", serverURL, err)

			message := "could not connect to MAAS controller - check the endpoint is correct"
			trimmedURL := strings.TrimRight(serverURL, "/")
			if !strings.HasSuffix(trimmedURL, "/MAAS") {
				message += " (it normally ends with /MAAS)"
			}
			return caps, errors.NewNotSupported(nil, message)
		}
	}
	if err != nil {
		logger.Debugf("Can't connect to maas server at endpoint %q: %v", serverURL, err)
		return caps, err
	}
	info, err := result.GetMap()
	if err != nil {
		logger.Debugf("Invalid data returned from maas endpoint %q: %v", serverURL, err)
		// invalid data of some sort, probably not a MAAS server.
		return caps, errors.New("failed to get expected data from server")
	}
	capsObj, ok := info["capabilities"]
	if !ok {
		return caps, fmt.Errorf("MAAS does not report capabilities")
	}
	items, err := capsObj.GetArray()
	if err != nil {
		logger.Debugf("Invalid data returned from maas endpoint %q: %v", serverURL, err)
		return caps, errors.New("failed to get expected data from server")
	}
	for _, item := range items {
		val, err := item.GetString()
		if err != nil {
			logger.Debugf("Invalid data returned from maas endpoint %q: %v", serverURL, err)
			return set.NewStrings(), errors.New("failed to get expected data from server")
		}
		caps.Add(val)
	}
	return caps, nil
}

// getMAASClient returns a MAAS client object to use for a request, in a
// lock-protected fashion.
func (env *maasEnviron) getMAASClient() *gomaasapi.MAASObject {
	env.ecfgMutex.Lock()
	defer env.ecfgMutex.Unlock()

	return env.maasClientUnlocked
}

var dashSuffix = regexp.MustCompile("^(.*)-\\d+$")

func spaceNamesToSpaceInfo(
	spaces []string, spaceMap map[string]corenetwork.SpaceInfo,
) ([]corenetwork.SpaceInfo, error) {
	var spaceInfos []corenetwork.SpaceInfo
	for _, name := range spaces {
		info, ok := spaceMap[name]
		if !ok {
			matches := dashSuffix.FindAllStringSubmatch(name, 1)
			if matches == nil {
				return nil, errors.Errorf("unrecognised space in constraint %q", name)
			}
			// A -number was added to the space name when we
			// converted to a juju name, we found
			info, ok = spaceMap[matches[0][1]]
			if !ok {
				return nil, errors.Errorf("unrecognised space in constraint %q", name)
			}
		}
		spaceInfos = append(spaceInfos, info)
	}
	return spaceInfos, nil
}

func (env *maasEnviron) buildSpaceMap(ctx context.ProviderCallContext) (map[string]corenetwork.SpaceInfo, error) {
	spaces, err := env.Spaces(ctx)
	if err != nil {
		return nil, errors.Trace(err)
	}
	spaceMap := make(map[string]corenetwork.SpaceInfo)
	empty := set.Strings{}
	for _, space := range spaces {
		jujuName := corenetwork.ConvertSpaceName(string(space.Name), empty)
		spaceMap[jujuName] = space
	}
	return spaceMap, nil
}

func (env *maasEnviron) spaceNamesToSpaceInfo(
	ctx context.ProviderCallContext, positiveSpaces, negativeSpaces []string,
) ([]corenetwork.SpaceInfo, []corenetwork.SpaceInfo, error) {
	spaceMap, err := env.buildSpaceMap(ctx)
	if err != nil {
		return nil, nil, errors.Trace(err)
	}

	positiveSpaceIds, err := spaceNamesToSpaceInfo(positiveSpaces, spaceMap)
	if err != nil {
		return nil, nil, errors.Trace(err)
	}
	negativeSpaceIds, err := spaceNamesToSpaceInfo(negativeSpaces, spaceMap)
	if err != nil {
		return nil, nil, errors.Trace(err)
	}
	return positiveSpaceIds, negativeSpaceIds, nil
}

// networkSpaceRequirements combines the space requirements for the application
// bindings and the specified constraints and returns a set of provider
// space IDs for which a NIC needs to be provisioned in the instance we are
// about to launch and a second (negative) set of space IDs that must not be
// present in the launched instance NICs.
func (env *maasEnviron) networkSpaceRequirements(ctx context.ProviderCallContext, endpointToProviderSpaceID map[string]corenetwork.Id, cons constraints.Value) (set.Strings, set.Strings, error) {
	positiveSpaceIds := set.NewStrings()
	negativeSpaceIds := set.NewStrings()

	// Iterate the application bindings and add each bound space ID to the
	// positive space set.
	for _, providerSpaceID := range endpointToProviderSpaceID {
		// The alpha space is not part of the MAAS space list. When the
		// code that maps between space IDs and provider space IDs
		// encounters a space that it cannot map, it passes the space
		// name through.
		if providerSpaceID == corenetwork.AlphaSpaceName {
			continue
		}

		positiveSpaceIds.Add(string(providerSpaceID))
	}

	// Convert space constraints into a list of space IDs to include and
	// a list of space IDs to omit.
	positiveSpaceNames, negativeSpaceNames := convertSpacesFromConstraints(cons.Spaces)
	positiveSpaceInfo, negativeSpaceInfo, err := env.spaceNamesToSpaceInfo(ctx, positiveSpaceNames, negativeSpaceNames)
	if err != nil {
		// Spaces are not supported by this MAAS instance.
		if errors.IsNotSupported(err) {
			return nil, nil, nil
		}

		common.HandleCredentialError(IsAuthorisationFailure, err, ctx)
		return nil, nil, errors.Trace(err)
	}

	// Append required space IDs from constraints.
	for _, si := range positiveSpaceInfo {
		if si.ProviderId == "" {
			continue
		}
		positiveSpaceIds.Add(string(si.ProviderId))
	}

	// Calculate negative space ID set and check for clashes with the positive set.
	for _, si := range negativeSpaceInfo {
		if si.ProviderId == "" {
			continue
		}

		if positiveSpaceIds.Contains(string(si.ProviderId)) {
			return nil, nil, errors.NewNotValid(nil, fmt.Sprintf("negative space %q from constraints clashes with required spaces for instance NICs", si.Name))
		}

		negativeSpaceIds.Add(string(si.ProviderId))
	}

	return positiveSpaceIds, negativeSpaceIds, nil
}

// acquireNode2 allocates a machine from MAAS2.
func (env *maasEnviron) acquireNode2(
	ctx context.ProviderCallContext,
	nodeName, zoneName, systemId string,
	cons constraints.Value,
	positiveSpaceIDs set.Strings,
	negativeSpaceIDs set.Strings,
	volumes []volumeInfo,
) (maasInstance, error) {
	acquireParams := convertConstraints2(cons)
	addInterfaces2(&acquireParams, positiveSpaceIDs, negativeSpaceIDs)
	addStorage2(&acquireParams, volumes)
	acquireParams.AgentName = env.uuid
	if zoneName != "" {
		acquireParams.Zone = zoneName
	}
	if nodeName != "" {
		acquireParams.Hostname = nodeName
	}
	if systemId != "" {
		acquireParams.SystemId = systemId
	}
	machine, constraintMatches, err := env.maasController.AllocateMachine(acquireParams)

	if err != nil {
		common.HandleCredentialError(IsAuthorisationFailure, err, ctx)
		return nil, errors.Trace(err)
	}
	return &maas2Instance{
		machine:           machine,
		constraintMatches: constraintMatches,
		environ:           env,
	}, nil
}

// acquireNode allocates a node from the MAAS.
func (env *maasEnviron) acquireNode(
	ctx context.ProviderCallContext,
	nodeName, zoneName, systemId string,
	cons constraints.Value,
	positiveSpaceIDs set.Strings,
	negativeSpaceIDs set.Strings,
	volumes []volumeInfo,
) (gomaasapi.MAASObject, error) {

	// TODO(axw) 2014-08-18 #1358219
	// We should be requesting preferred architectures if unspecified,
	// like in the other providers.
	//
	// This is slightly complicated in MAAS as there are a finite
	// number of each architecture; preference may also conflict with
	// other constraints, such as tags. Thus, a preference becomes a
	// demand (which may fail) if not handled properly.

	acquireParams := convertConstraints(cons)
	addInterfaces(acquireParams, positiveSpaceIDs, negativeSpaceIDs)
	addStorage(acquireParams, volumes)
	acquireParams.Add("agent_name", env.uuid)
	if zoneName != "" {
		acquireParams.Add("zone", zoneName)
	}
	if nodeName != "" {
		acquireParams.Add("name", nodeName)
	}
	if systemId != "" {
		acquireParams.Add("system_id", systemId)
	}

	var (
		result gomaasapi.JSONObject
		err    error
	)
	for a := shortAttempt.Start(); a.Next(); {
		client := env.getMAASClient().GetSubObject("nodes/")
		logger.Tracef("calling acquire with params: %+v", acquireParams)
		if result, err = client.CallPost("acquire", acquireParams); err == nil {
			break // Got a result back.
		}
	}
	if err != nil {
		return gomaasapi.MAASObject{}, err
	}
	node, err := result.GetMAASObject()
	if err != nil {
		err := errors.Annotate(err, "unexpected result from 'acquire' on MAAS API")
		return gomaasapi.MAASObject{}, err
	}
	return node, nil
}

func (env *maasEnviron) startNode2(node maas2Instance, series string, userdata []byte) (*maas2Instance, error) {
	err := node.machine.Start(gomaasapi.StartArgs{DistroSeries: series, UserData: string(userdata)})
	if err != nil {
		return nil, errors.Trace(err)
	}
	// Machine.Start updates the machine in-place when it succeeds.
	return &maas2Instance{machine: node.machine}, nil

}

// DistributeInstances implements the state.InstanceDistributor policy.
func (env *maasEnviron) DistributeInstances(
	ctx context.ProviderCallContext, candidates, distributionGroup []instance.Id, limitZones []string,
) ([]instance.Id, error) {
	return common.DistributeInstances(env, ctx, candidates, distributionGroup, limitZones)
}

// StartInstance is specified in the InstanceBroker interface.
func (env *maasEnviron) StartInstance(
	ctx context.ProviderCallContext,
	args environs.StartInstanceParams,
) (_ *environs.StartInstanceResult, err error) {

	availabilityZone := args.AvailabilityZone
	var nodeName, systemId string
	if args.Placement != "" {
		placement, err := env.parsePlacement(ctx, args.Placement)
		if err != nil {
			return nil, common.ZoneIndependentError(err)
		}
		// NOTE(axw) we wipe out args.AvailabilityZone if the
		// user specified a specific node or system ID via
		// placement, as placement must always take precedence.
		switch {
		case placement.systemId != "":
			availabilityZone = ""
			systemId = placement.systemId
		case placement.nodeName != "":
			availabilityZone = ""
			nodeName = placement.nodeName
		}
	}
	if availabilityZone != "" {
		zones, err := env.AvailabilityZones(ctx)
		if err != nil {
			return nil, errors.Trace(err)
		}
		if err := errors.Trace(zones.Validate(availabilityZone)); err != nil {
			return nil, errors.Trace(err)
		}
		logger.Debugf("attempting to acquire node in zone %q", availabilityZone)
	}

	// Storage.
	volumes, err := buildMAASVolumeParameters(args.Volumes, args.Constraints)
	if err != nil {
		return nil, common.ZoneIndependentError(errors.Annotate(err, "invalid volume parameters"))
	}

	// Calculate network space requirements.
	positiveSpaceIDs, negativeSpaceIDs, err := env.networkSpaceRequirements(ctx, args.EndpointBindings, args.Constraints)
	if err != nil {
		return nil, errors.Trace(err)
	}

	inst, selectNodeErr := env.selectNode(ctx,
		selectNodeArgs{
			Constraints:      args.Constraints,
			AvailabilityZone: availabilityZone,
			NodeName:         nodeName,
			SystemId:         systemId,
			PositiveSpaceIDs: positiveSpaceIDs,
			NegativeSpaceIDs: negativeSpaceIDs,
			Volumes:          volumes,
		})
	if selectNodeErr != nil {
		err := errors.Annotate(selectNodeErr, "failed to acquire node")
		if selectNodeErr.noMatch && availabilityZone != "" {
			// The error was due to MAAS not being able to
			// find provide a machine matching the specified
			// constraints in the zone; try again in another.
			return nil, errors.Trace(err)
		}
		return nil, common.ZoneIndependentError(err)
	}

	defer func() {
		if err != nil {
			if err := env.StopInstances(ctx, inst.Id()); err != nil {
				logger.Errorf("error releasing failed instance: %v", err)
			}
		}
	}()

	hc, err := inst.hardwareCharacteristics()
	if err != nil {
		return nil, common.ZoneIndependentError(err)
	}

	selectedTools, err := args.Tools.Match(tools.Filter{
		Arch: *hc.Arch,
	})
	if err != nil {
		return nil, common.ZoneIndependentError(err)
	}
	if err := args.InstanceConfig.SetTools(selectedTools); err != nil {
		return nil, common.ZoneIndependentError(err)
	}

	hostname, err := inst.hostname()
	if err != nil {
		return nil, common.ZoneIndependentError(err)
	}

	if err := instancecfg.FinishInstanceConfig(args.InstanceConfig, env.Config()); err != nil {
		return nil, common.ZoneIndependentError(err)
	}

	subnetsMap, err := env.subnetToSpaceIds(ctx)
	if err != nil {
		return nil, common.ZoneIndependentError(err)
	}

	series := args.InstanceConfig.Series
	cloudcfg, err := env.newCloudinitConfig(hostname, series)
	if err != nil {
		return nil, common.ZoneIndependentError(err)
	}

	userdata, err := providerinit.ComposeUserData(args.InstanceConfig, cloudcfg, MAASRenderer{})
	if err != nil {
		return nil, common.ZoneIndependentError(errors.Annotate(
			err, "could not compose userdata for bootstrap node",
		))
	}
	logger.Debugf("maas user data; %d bytes", len(userdata))

	var displayName string
	var interfaces corenetwork.InterfaceInfos
	inst2 := inst.(*maas2Instance)
	startedInst, err := env.startNode2(*inst2, series, userdata)
	if err != nil {
		return nil, common.ZoneIndependentError(err)
	}
	domains, err := env.Domains(ctx)
	if err != nil {
		return nil, errors.Trace(err)
	}
	interfaces, err = maasNetworkInterfaces(ctx, startedInst, subnetsMap, domains...)
	if err != nil {
		return nil, common.ZoneIndependentError(err)
	}
	env.tagInstance2(inst2, args.InstanceConfig)

	displayName, err = inst2.displayName()
	if err != nil {
		return nil, common.ZoneIndependentError(err)
	}
	logger.Debugf("started instance %q", inst.Id())

	requestedVolumes := make([]names.VolumeTag, len(args.Volumes))
	for i, v := range args.Volumes {
		requestedVolumes[i] = v.Tag
	}
	resultVolumes, resultAttachments, err := inst.volumes(
		names.NewMachineTag(args.InstanceConfig.MachineId),
		requestedVolumes,
	)
	if err != nil {
		return nil, common.ZoneIndependentError(err)
	}
	if len(resultVolumes) != len(requestedVolumes) {
		return nil, common.ZoneIndependentError(errors.Errorf(
			"requested %v storage volumes. %v returned",
			len(requestedVolumes), len(resultVolumes),
		))
	}

	return &environs.StartInstanceResult{
		DisplayName:       displayName,
		Instance:          inst,
		Hardware:          hc,
		NetworkInfo:       interfaces,
		Volumes:           resultVolumes,
		VolumeAttachments: resultAttachments,
	}, nil
}

func (env *maasEnviron) tagInstance2(inst *maas2Instance, instanceConfig *instancecfg.InstanceConfig) {
	err := inst.machine.SetOwnerData(instanceConfig.Tags)
	if err != nil {
		logger.Errorf("could not set owner data for instance: %v", err)
	}
}

func (env *maasEnviron) waitForNodeDeployment(ctx context.ProviderCallContext, id instance.Id, timeout time.Duration) error {
	// TODO(katco): 2016-08-09: lp:1611427
	longAttempt := utils.AttemptStrategy{
		Delay: 10 * time.Second,
		Total: timeout,
	}

	retryCount := 1
	for a := longAttempt.Start(); a.Next(); {
		machine, err := env.getInstance(ctx, id)
		if err != nil {
			logger.Warningf("failed to get instance from provider attempt %d", retryCount)
			if denied := common.MaybeHandleCredentialError(IsAuthorisationFailure, err, ctx); denied {
				break
			}

			retryCount++
			continue
		}
		stat := machine.Status(ctx)
		if stat.Status == status.Running {
			return nil
		}
		if stat.Status == status.ProvisioningError {
			return errors.Errorf("instance %q failed to deploy", id)

		}
	}
	return errors.Errorf("instance %q is started but not deployed", id)
}

func (env *maasEnviron) deploymentStatusOne(ctx context.ProviderCallContext, id instance.Id) (string, string) {
	results, err := env.deploymentStatus(ctx, id)
	if err != nil {
		common.HandleCredentialError(IsAuthorisationFailure, err, ctx)
		return "", ""
	}
	systemId := extractSystemId(id)
	substatus := env.getDeploymentSubstatus(ctx, systemId)
	return results[systemId], substatus
}

func (env *maasEnviron) getDeploymentSubstatus(ctx context.ProviderCallContext, systemId string) string {
	nodesAPI := env.getMAASClient().GetSubObject("nodes")
	result, err := nodesAPI.CallGet("list", nil)
	if err != nil {
		common.HandleCredentialError(IsAuthorisationFailure, err, ctx)
		return ""
	}
	slices, err := result.GetArray()
	if err != nil {
		return ""
	}
	for _, slice := range slices {
		resultMap, err := slice.GetMap()
		if err != nil {
			continue
		}
		sysId, err := resultMap["system_id"].GetString()
		if err != nil {
			continue
		}
		if sysId == systemId {
			message, err := resultMap["substatus_message"].GetString()
			if err != nil {
				logger.Warningf("could not get string for substatus_message: %v", resultMap["substatus_message"])
				return ""
			}
			return message
		}
	}

	return ""
}

// deploymentStatus returns the deployment state of MAAS instances with
// the specified Juju instance ids.
// Note: the result is a map of MAAS systemId to state.
func (env *maasEnviron) deploymentStatus(ctx context.ProviderCallContext, ids ...instance.Id) (map[string]string, error) {
	nodesAPI := env.getMAASClient().GetSubObject("nodes")
	result, err := DeploymentStatusCall(nodesAPI, ids...)
	if err != nil {
		if err, ok := errors.Cause(err).(gomaasapi.ServerError); ok && err.StatusCode == http.StatusBadRequest {
			return nil, errors.NewNotImplemented(err, "deployment status")
		}
		common.HandleCredentialError(IsAuthorisationFailure, err, ctx)
		return nil, errors.Trace(err)
	}
	resultMap, err := result.GetMap()
	if err != nil {
		return nil, errors.Trace(err)
	}
	statusValues := make(map[string]string)
	for systemId, jsonValue := range resultMap {
		sts, err := jsonValue.GetString()
		if err != nil {
			return nil, errors.Trace(err)
		}
		statusValues[systemId] = sts
	}
	return statusValues, nil
}

func deploymentStatusCall(nodes gomaasapi.MAASObject, ids ...instance.Id) (gomaasapi.JSONObject, error) {
	filter := getSystemIdValues("nodes", ids)
	return nodes.CallGet("deployment_status", filter)
}

type selectNodeArgs struct {
	AvailabilityZone string
	NodeName         string
	SystemId         string
	Constraints      constraints.Value
	PositiveSpaceIDs set.Strings
	NegativeSpaceIDs set.Strings
	Volumes          []volumeInfo
}

type selectNodeError struct {
	error
	noMatch bool
}

func (env *maasEnviron) selectNode(ctx context.ProviderCallContext, args selectNodeArgs) (maasInstance, *selectNodeError) {
	inst, err := env.acquireNode2(
		ctx,
		args.NodeName,
		args.AvailabilityZone,
		args.SystemId,
		args.Constraints,
		args.PositiveSpaceIDs,
		args.NegativeSpaceIDs,
		args.Volumes,
	)
	if err != nil {
		return nil, &selectNodeError{
			error:   errors.Trace(err),
			noMatch: gomaasapi.IsNoMatchError(err),
		}
	}
	return inst, nil
}

// newCloudinitConfig creates a cloudinit.Config structure suitable as a base
// for initialising a MAAS node.
func (env *maasEnviron) newCloudinitConfig(hostname, forSeries string) (cloudinit.CloudConfig, error) {
	cloudcfg, err := cloudinit.New(forSeries)
	if err != nil {
		return nil, err
	}

	info := machineInfo{hostname}
	runCmd, err := info.cloudinitRunCmd(cloudcfg)
	if err != nil {
		return nil, errors.Trace(err)
	}

	operatingSystem, err := series.GetOSFromSeries(forSeries)
	if err != nil {
		return nil, errors.Trace(err)
	}
	switch operatingSystem {
	case os.Windows:
		cloudcfg.AddScripts(runCmd)
	case os.Ubuntu:
		cloudcfg.SetSystemUpdate(true)
		cloudcfg.AddScripts("set -xe", runCmd)
		// DisableNetworkManagement can still disable the bridge(s) creation.
		if on, set := env.Config().DisableNetworkManagement(); on && set {
			logger.Infof(
				"network management disabled - not using %q bridge for containers",
				instancecfg.DefaultBridgeName,
			)
			break
		}
		cloudcfg.AddPackage("bridge-utils")
	}
	return cloudcfg, nil
}

func (env *maasEnviron) releaseNodes2(ctx context.ProviderCallContext, ids []instance.Id, recurse bool) error {
	args := gomaasapi.ReleaseMachinesArgs{
		SystemIDs: instanceIdsToSystemIDs(ids),
		Comment:   "Released by Juju MAAS provider",
	}
	err := env.maasController.ReleaseMachines(args)

	denied := common.MaybeHandleCredentialError(IsAuthorisationFailure, err, ctx)
	switch {
	case err == nil:
		return nil
	case gomaasapi.IsCannotCompleteError(err):
		// CannotCompleteError means a node couldn't be released due to
		// a state conflict. Likely it's already released or disk
		// erasing. We're assuming this error *only* means it's
		// safe to assume the instance is already released.
		// MaaS also releases (or attempts) all nodes, and raises
		// a single error on failure. So even with an error 409, all
		// nodes have been released.
		logger.Infof("ignoring error while releasing nodes (%v); all nodes released OK", err)
		return nil
	case gomaasapi.IsBadRequestError(err), denied:
		// a status code of 400 or 403 means one of the nodes
		// couldn't be found and none have been released. We have to
		// release all the ones we can individually.
		if !recurse {
			// this node has already been released and we're golden
			return nil
		}
		return env.releaseNodesIndividually(ctx, ids)

	default:
		return errors.Annotatef(err, "cannot release nodes")
	}
}

func (env *maasEnviron) releaseNodesIndividually(ctx context.ProviderCallContext, ids []instance.Id) error {
	var lastErr error
	for _, id := range ids {
		err := env.releaseNodes2(ctx, []instance.Id{id}, false)
		if err != nil {
			lastErr = err
			logger.Errorf("error while releasing node %v (%v)", id, err)
			if denied := common.MaybeHandleCredentialError(IsAuthorisationFailure, err, ctx); denied {
				break
			}
		}
	}
	return errors.Trace(lastErr)
}

func instanceIdsToSystemIDs(ids []instance.Id) []string {
	systemIDs := make([]string, len(ids))
	for index, id := range ids {
		systemIDs[index] = string(id)
	}
	return systemIDs
}

// StopInstances is specified in the InstanceBroker interface.
func (env *maasEnviron) StopInstances(ctx context.ProviderCallContext, ids ...instance.Id) error {
	// Shortcut to exit quickly if 'instances' is an empty slice or nil.
	if len(ids) == 0 {
		return nil
	}

	err := env.releaseNodes2(ctx, ids, true)
	if err != nil {
		return errors.Trace(err)
	}
	return common.RemoveStateInstances(env.Storage(), ids...)

}

// Instances returns the instances.Instance objects corresponding to the given
// slice of instance.Id.  The error is ErrNoInstances if no instances
// were found.
func (env *maasEnviron) Instances(ctx context.ProviderCallContext, ids []instance.Id) ([]instances.Instance, error) {
	if len(ids) == 0 {
		// This would be treated as "return all instances" below, so
		// treat it as a special case.
		// The interface requires us to return this particular error
		// if no instances were found.
		return nil, environs.ErrNoInstances
	}
	acquired, err := env.acquiredInstances(ctx, ids)
	if err != nil {
		return nil, errors.Trace(err)
	}
	if len(acquired) == 0 {
		return nil, environs.ErrNoInstances
	}

	idMap := make(map[instance.Id]instances.Instance)
	for _, inst := range acquired {
		idMap[inst.Id()] = inst
	}

	missing := false
	result := make([]instances.Instance, len(ids))
	for index, id := range ids {
		val, ok := idMap[id]
		if !ok {
			missing = true
			continue
		}
		result[index] = val
	}

	if missing {
		return result, environs.ErrPartialInstances
	}
	return result, nil
}

// acquireInstances calls the MAAS API to list acquired nodes.
//
// The "ids" slice is a filter for specific instance IDs.
// Due to how this works in the HTTP API, an empty "ids"
// matches all instances (not none as you might expect).
func (env *maasEnviron) acquiredInstances(ctx context.ProviderCallContext, ids []instance.Id) ([]instances.Instance, error) {
	args := gomaasapi.MachinesArgs{
		AgentName: env.uuid,
		SystemIDs: instanceIdsToSystemIDs(ids),
	}

	inst, err := env.instances(ctx, args)
	return inst, errors.Trace(err)
}

func (env *maasEnviron) instances(ctx context.ProviderCallContext, args gomaasapi.MachinesArgs) ([]instances.Instance, error) {
	machines, err := env.maasController.Machines(args)
	if err != nil {
		common.HandleCredentialError(IsAuthorisationFailure, err, ctx)
		return nil, errors.Trace(err)
	}

	inst := make([]instances.Instance, len(machines))
	for index, machine := range machines {
		inst[index] = &maas2Instance{machine: machine, environ: env}
	}
	return inst, nil
}

// subnetsFromNode fetches all the subnets for a specific node.
func (env *maasEnviron) subnetsFromNode(ctx context.ProviderCallContext, nodeId string) ([]gomaasapi.JSONObject, error) {
	client := env.getMAASClient().GetSubObject("nodes").GetSubObject(nodeId)
	json, err := client.CallGet("", nil)
	if err != nil {
		if maasErr, ok := errors.Cause(err).(gomaasapi.ServerError); ok && maasErr.StatusCode == http.StatusNotFound {
			return nil, errors.NotFoundf("intance %q", nodeId)
		}
		common.HandleCredentialError(IsAuthorisationFailure, err, ctx)
		return nil, errors.Trace(err)
	}
	nodeMap, err := json.GetMap()
	if err != nil {
		return nil, errors.Trace(err)
	}
	interfacesArray, err := nodeMap["interface_set"].GetArray()
	if err != nil {
		return nil, errors.Trace(err)
	}
	var subnets []gomaasapi.JSONObject
	for _, iface := range interfacesArray {
		ifaceMap, err := iface.GetMap()
		if err != nil {
			return nil, errors.Trace(err)
		}
		linksArray, err := ifaceMap["links"].GetArray()
		if err != nil {
			return nil, errors.Trace(err)
		}
		for _, link := range linksArray {
			linkMap, err := link.GetMap()
			if err != nil {
				return nil, errors.Trace(err)
			}
			subnet, ok := linkMap["subnet"]
			if !ok {
				return nil, errors.New("subnet not found")
			}
			subnets = append(subnets, subnet)
		}
	}
	return subnets, nil
}

// subnetFromJson populates a network.SubnetInfo from a gomaasapi.JSONObject
// representing a single subnet. This can come from either the subnets api
// endpoint or the node endpoint.
func (env *maasEnviron) subnetFromJson(
	subnet gomaasapi.JSONObject, spaceId corenetwork.Id,
) (corenetwork.SubnetInfo, error) {
	var subnetInfo corenetwork.SubnetInfo
	fields, err := subnet.GetMap()
	if err != nil {
		return subnetInfo, errors.Trace(err)
	}
	subnetIdFloat, err := fields["id"].GetFloat64()
	if err != nil {
		return subnetInfo, errors.Annotatef(err, "cannot get subnet Id")
	}
	subnetId := strconv.Itoa(int(subnetIdFloat))
	cidr, err := fields["cidr"].GetString()
	if err != nil {
		return subnetInfo, errors.Annotatef(err, "cannot get cidr")
	}
	vid := 0
	vidField, ok := fields["vid"]
	if ok && !vidField.IsNil() {
		// vid is optional, so assume it's 0 when missing or nil.
		vidFloat, err := vidField.GetFloat64()
		if err != nil {
			return subnetInfo, errors.Errorf("cannot get vlan tag: %v", err)
		}
		vid = int(vidFloat)
	}

	subnetInfo = corenetwork.SubnetInfo{
		ProviderId:      corenetwork.Id(subnetId),
		VLANTag:         vid,
		CIDR:            cidr,
		ProviderSpaceId: spaceId,
	}
	return subnetInfo, nil
}

// filteredSubnets fetches subnets, filtering optionally by nodeId and/or a
// slice of subnetIds. If subnetIds is empty then all subnets for that node are
// fetched. If nodeId is empty, all subnets are returned (filtering by subnetIds
// first, if set).
func (env *maasEnviron) filteredSubnets(
	ctx context.ProviderCallContext, nodeId string, subnetIds []corenetwork.Id,
) ([]corenetwork.SubnetInfo, error) {
	var jsonNets []gomaasapi.JSONObject
	var err error
	if nodeId != "" {
		jsonNets, err = env.subnetsFromNode(ctx, nodeId)
		if err != nil {
			return nil, errors.Trace(err)
		}
	} else {
		jsonNets, err = env.fetchAllSubnets(ctx)
		if err != nil {
			return nil, errors.Trace(err)
		}
	}
	subnetIdSet := make(map[string]bool)
	for _, netId := range subnetIds {
		subnetIdSet[string(netId)] = false
	}

	subnetsMap, err := env.subnetToSpaceIds(ctx)
	if err != nil {
		return nil, errors.Trace(err)
	}

	var subnets []corenetwork.SubnetInfo
	for _, jsonNet := range jsonNets {
		fields, err := jsonNet.GetMap()
		if err != nil {
			return nil, err
		}
		subnetIdFloat, err := fields["id"].GetFloat64()
		if err != nil {
			return nil, errors.Annotate(err, "cannot get subnet Id")
		}
		subnetId := strconv.Itoa(int(subnetIdFloat))
		// If we're filtering by subnet id check if this subnet is one
		// we're looking for.
		if len(subnetIds) != 0 {
			_, ok := subnetIdSet[subnetId]
			if !ok {
				// This id is not what we're looking for.
				continue
			}
			subnetIdSet[subnetId] = true
		}
		cidr, err := fields["cidr"].GetString()
		if err != nil {
			return nil, errors.Annotatef(err, "cannot get subnet %q cidr", subnetId)
		}
		spaceId, ok := subnetsMap[cidr]
		if !ok {
			logger.Warningf("unrecognised subnet: %q, setting empty space id", cidr)
			spaceId = network.UnknownId
		}

		subnetInfo, err := env.subnetFromJson(jsonNet, spaceId)
		if err != nil {
			return nil, errors.Trace(err)
		}
		subnets = append(subnets, subnetInfo)
		logger.Tracef("found subnet with info %#v", subnetInfo)
	}
	return subnets, checkNotFound(subnetIdSet)
}

func (env *maasEnviron) getInstance(ctx context.ProviderCallContext, instId instance.Id) (instances.Instance, error) {
	instances, err := env.acquiredInstances(ctx, []instance.Id{instId})
	if err != nil {
		// This path can never trigger on MAAS 2, but MAAS 2 doesn't
		// return an error for a machine not found, it just returns
		// empty results. The clause below catches that.
		if maasErr, ok := errors.Cause(err).(gomaasapi.ServerError); ok && maasErr.StatusCode == http.StatusNotFound {
			return nil, errors.NotFoundf("instance %q", instId)
		}
		return nil, errors.Annotatef(err, "getting instance %q", instId)
	}
	if len(instances) == 0 {
		return nil, errors.NotFoundf("instance %q", instId)
	}
	inst := instances[0]
	return inst, nil
}

// fetchAllSubnets calls the MAAS subnets API to get all subnets and returns the
// JSON response or an error. If capNetworkDeploymentUbuntu is not available, an
// error satisfying errors.IsNotSupported will be returned.
func (env *maasEnviron) fetchAllSubnets(ctx context.ProviderCallContext) ([]gomaasapi.JSONObject, error) {
	client := env.getMAASClient().GetSubObject("subnets")

	json, err := client.CallGet("", nil)
	if err != nil {
		common.HandleCredentialError(IsAuthorisationFailure, err, ctx)
		return nil, errors.Trace(err)
	}
	return json.GetArray()
}

// subnetToSpaceIds fetches the spaces from MAAS and builds a map of subnets to
// space ids.
func (env *maasEnviron) subnetToSpaceIds(ctx context.ProviderCallContext) (map[string]corenetwork.Id, error) {
	subnetsMap := make(map[string]corenetwork.Id)
	spaces, err := env.Spaces(ctx)
	if err != nil {
		return subnetsMap, errors.Trace(err)
	}
	for _, space := range spaces {
		for _, subnet := range space.Subnets {
			subnetsMap[subnet.CIDR] = space.ProviderId
		}
	}
	return subnetsMap, nil
}

// Spaces returns all the spaces, that have subnets, known to the provider.
// Space name is not filled in as the provider doesn't know the juju name for
// the space.
func (env *maasEnviron) Spaces(ctx context.ProviderCallContext) (corenetwork.SpaceInfos, error) {
	spaces, err := env.maasController.Spaces()
	if err != nil {
		common.HandleCredentialError(IsAuthorisationFailure, err, ctx)
		return nil, errors.Trace(err)
	}
	var result []corenetwork.SpaceInfo
	for _, space := range spaces {
		if len(space.Subnets()) == 0 {
			continue
		}
		outSpace := corenetwork.SpaceInfo{
			Name:       corenetwork.SpaceName(space.Name()),
			ProviderId: corenetwork.Id(strconv.Itoa(space.ID())),
			Subnets:    make([]corenetwork.SubnetInfo, len(space.Subnets())),
		}
		for i, subnet := range space.Subnets() {
			subnetInfo := corenetwork.SubnetInfo{
				ProviderId:      corenetwork.Id(strconv.Itoa(subnet.ID())),
				VLANTag:         subnet.VLAN().VID(),
				CIDR:            subnet.CIDR(),
				ProviderSpaceId: corenetwork.Id(strconv.Itoa(space.ID())),
			}
			outSpace.Subnets[i] = subnetInfo
		}
		result = append(result, outSpace)
	}
	return result, nil
}

// Subnets returns basic information about the specified subnets known
// by the provider for the specified instance. subnetIds must not be
// empty. Implements NetworkingEnviron.Subnets.
func (env *maasEnviron) Subnets(
	ctx context.ProviderCallContext, instId instance.Id, subnetIds []corenetwork.Id,
) ([]corenetwork.SubnetInfo, error) {
	var subnets []corenetwork.SubnetInfo
	if instId == instance.UnknownId {
		spaces, err := env.Spaces(ctx)
		if err != nil {
			return nil, errors.Trace(err)
		}
		for _, space := range spaces {
			subnets = append(subnets, space.Subnets...)
		}
	} else {
		var err error
		subnets, err = env.filteredSubnets2(ctx, instId)
		if err != nil {
			return nil, errors.Trace(err)
		}
	}

	if len(subnetIds) == 0 {
		return subnets, nil
	}
	var result []corenetwork.SubnetInfo
	subnetMap := make(map[string]bool)
	for _, subnetId := range subnetIds {
		subnetMap[string(subnetId)] = false
	}
	for _, subnet := range subnets {
		_, ok := subnetMap[string(subnet.ProviderId)]
		if !ok {
			// This id is not what we're looking for.
			continue
		}
		subnetMap[string(subnet.ProviderId)] = true
		result = append(result, subnet)
	}

	return result, checkNotFound(subnetMap)
}

func (env *maasEnviron) filteredSubnets2(
	ctx context.ProviderCallContext, instId instance.Id,
) ([]corenetwork.SubnetInfo, error) {
	args := gomaasapi.MachinesArgs{
		AgentName: env.uuid,
		SystemIDs: []string{string(instId)},
	}
	machines, err := env.maasController.Machines(args)
	if err != nil {
		common.HandleCredentialError(IsAuthorisationFailure, err, ctx)
		return nil, errors.Trace(err)
	}
	if len(machines) == 0 {
		return nil, errors.NotFoundf("machine %v", instId)
	} else if len(machines) > 1 {
		return nil, errors.Errorf("unexpected response getting machine details %v: %v", instId, machines)
	}

	machine := machines[0]
	spaceMap, err := env.buildSpaceMap(ctx)
	if err != nil {
		return nil, errors.Trace(err)
	}
	var result []corenetwork.SubnetInfo
	for _, iface := range machine.InterfaceSet() {
		for _, link := range iface.Links() {
			subnet := link.Subnet()
			space, ok := spaceMap[subnet.Space()]
			if !ok {
				return nil, errors.Errorf("missing space %v on subnet %v", subnet.Space(), subnet.CIDR())
			}
			subnetInfo := corenetwork.SubnetInfo{
				ProviderId:      corenetwork.Id(strconv.Itoa(subnet.ID())),
				VLANTag:         subnet.VLAN().VID(),
				CIDR:            subnet.CIDR(),
				ProviderSpaceId: space.ProviderId,
			}
			result = append(result, subnetInfo)
		}
	}
	return result, nil
}

func checkNotFound(subnetIdSet map[string]bool) error {
	var notFound []string
	for subnetId, found := range subnetIdSet {
		if !found {
			notFound = append(notFound, subnetId)
		}
	}
	if len(notFound) != 0 {
		return errors.Errorf("failed to find the following subnets: %v", strings.Join(notFound, ", "))
	}
	return nil
}

// AllInstances implements environs.InstanceBroker.
func (env *maasEnviron) AllInstances(ctx context.ProviderCallContext) ([]instances.Instance, error) {
	return env.acquiredInstances(ctx, nil)
}

// AllRunningInstances implements environs.InstanceBroker.
func (env *maasEnviron) AllRunningInstances(ctx context.ProviderCallContext) ([]instances.Instance, error) {
	// We always get all instances here, so "all" is the same as "running".
	return env.AllInstances(ctx)
}

// Storage is defined by the Environ interface.
func (env *maasEnviron) Storage() storage.Storage {
	env.ecfgMutex.Lock()
	defer env.ecfgMutex.Unlock()
	return env.storageUnlocked
}

func (env *maasEnviron) Destroy(ctx context.ProviderCallContext) error {
	if err := common.Destroy(env, ctx); err != nil {
		return errors.Trace(err)
	}
	return env.Storage().RemoveAll()
}

// DestroyController implements the Environ interface.
func (env *maasEnviron) DestroyController(ctx context.ProviderCallContext, controllerUUID string) error {
	// TODO(wallyworld): destroy hosted model resources
	return env.Destroy(ctx)
}

func (*maasEnviron) Provider() environs.EnvironProvider {
	return &providerInstance
}

func (env *maasEnviron) AllocateContainerAddresses(ctx context.ProviderCallContext, hostInstanceID instance.Id, containerTag names.MachineTag, preparedInfo corenetwork.InterfaceInfos) (corenetwork.InterfaceInfos, error) {
	if len(preparedInfo) == 0 {
		return nil, errors.Errorf("no prepared info to allocate")
	}

	logger.Debugf("using prepared container info: %+v", preparedInfo)
	args := gomaasapi.MachinesArgs{
		AgentName: env.uuid,
		SystemIDs: []string{string(hostInstanceID)},
	}
	machines, err := env.maasController.Machines(args)
	if err != nil {
		return nil, errors.Trace(err)
	}
	if len(machines) != 1 {
		return nil, errors.Errorf("failed to identify unique machine with ID %q; got %v", hostInstanceID, machines)
	}
	machine := machines[0]
	deviceName, err := env.namespace.Hostname(containerTag.Id())
	if err != nil {
		return nil, errors.Trace(err)
	}
	params, err := env.prepareDeviceDetails(deviceName, machine, preparedInfo)
	if err != nil {
		return nil, errors.Trace(err)
	}

	// Check to see if we've already tried to allocate information for this device:
	device, err := env.checkForExistingDevice(params)
	if err != nil {
		return nil, errors.Trace(err)
	}
	if device == nil {
		device, err = env.createAndPopulateDevice(params)
		if err != nil {
			return nil, errors.Annotatef(err,
				"failed to create MAAS device for %q",
				params.Name)
		}
	}

	// TODO(jam): the old code used to reload the device from its SystemID()
	nameToParentName := make(map[string]string)
	for _, nic := range preparedInfo {
		nameToParentName[nic.InterfaceName] = nic.ParentInterfaceName
	}
	interfaces, err := env.deviceInterfaceInfo2(device, nameToParentName, params.CIDRToStaticRoutes)
	if err != nil {
		return nil, errors.Annotate(err, "cannot get device interfaces")
	}
	return interfaces, nil
}

func (env *maasEnviron) ReleaseContainerAddresses(ctx context.ProviderCallContext, interfaces []corenetwork.ProviderInterfaceInfo) error {
	hwAddresses := make([]string, len(interfaces))
	for i, info := range interfaces {
		hwAddresses[i] = info.HardwareAddress
	}

	devices, err := env.maasController.Devices(gomaasapi.DevicesArgs{MACAddresses: hwAddresses})
	if err != nil {
		common.HandleCredentialError(IsAuthorisationFailure, err, ctx)
		return errors.Trace(err)
	}
	// If one device matched on multiple MAC addresses (like for
	// multi-nic containers) it will be in the slice multiple
	// times. Skip devices we've seen already.
	seen := set.NewStrings()
	for _, device := range devices {
		if seen.Contains(device.SystemID()) {
			continue
		}
		seen.Add(device.SystemID())

		err = device.Delete()
		if err != nil {
			return errors.Annotatef(err, "deleting device %s", device.SystemID())
		}
	}
	return nil
}

// AdoptResources updates all the instances to indicate they
// are now associated with the specified controller.
func (env *maasEnviron) AdoptResources(ctx context.ProviderCallContext, controllerUUID string, _ version.Number) error {
	instances, err := env.AllInstances(ctx)
	if err != nil {
		return errors.Trace(err)
	}
	var failed []instance.Id
	for _, inst := range instances {
		maas2Instance, ok := inst.(*maas2Instance)
		if !ok {
			// This should never happen.
			return errors.Errorf("instance %q wasn't a maas2Instance", inst.Id())
		}
		// From the MAAS docs: "[SetOwnerData] will not remove any
		// previous keys unless explicitly passed with an empty
		// string." So not passing all of the keys here is fine.
		// https://maas.ubuntu.com/docs2.0/api.html#machine
		err := maas2Instance.machine.SetOwnerData(map[string]string{tags.JujuController: controllerUUID})
		if err != nil {
			logger.Errorf("error setting controller uuid tag for %q: %v", inst.Id(), err)
			failed = append(failed, inst.Id())
		}
	}

	if failed != nil {
		return errors.Errorf("failed to update controller for some instances: %v", failed)
	}
	return nil
}

// ProviderSpaceInfo implements environs.NetworkingEnviron.
func (*maasEnviron) ProviderSpaceInfo(
	ctx context.ProviderCallContext, space *corenetwork.SpaceInfo,
) (*environs.ProviderSpaceInfo, error) {
	return nil, errors.NotSupportedf("provider space info")
}

// AreSpacesRoutable implements environs.NetworkingEnviron.
func (*maasEnviron) AreSpacesRoutable(ctx context.ProviderCallContext, space1, space2 *environs.ProviderSpaceInfo) (bool, error) {
	return false, nil
}

// SSHAddresses implements environs.SSHAddresses.
func (*maasEnviron) SSHAddresses(ctx context.ProviderCallContext, addresses corenetwork.SpaceAddresses) (corenetwork.SpaceAddresses, error) {
	return addresses, nil
}

// SuperSubnets implements environs.SuperSubnets
func (*maasEnviron) SuperSubnets(ctx context.ProviderCallContext) ([]string, error) {
	return nil, errors.NotSupportedf("super subnets")
}

// Domains gets the domains managed by MAAS. We only need the name of the
// domain at present. If more information is needed this function can be
// updated to parse and return a structure. Client code would need to be
// updated.
func (env *maasEnviron) Domains(ctx context.ProviderCallContext) ([]string, error) {
	maasDomains, err := env.maasController.Domains()
	if err != nil {
		common.HandleCredentialError(IsAuthorisationFailure, err, ctx)
		return nil, errors.Trace(err)
	}
	var result []string
	for _, domain := range maasDomains {
		result = append(result, domain.Name())
	}
	return result, nil
}
