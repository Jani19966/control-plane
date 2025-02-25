package broker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/kyma-project/control-plane/components/kyma-environment-broker/internal/euaccess"

	"k8s.io/client-go/tools/clientcmd"

	"github.com/google/uuid"
	"github.com/kyma-incubator/compass/components/director/pkg/jsonschema"
	"github.com/kyma-project/control-plane/components/kyma-environment-broker/common/gardener"
	"github.com/kyma-project/control-plane/components/kyma-environment-broker/internal"
	"github.com/kyma-project/control-plane/components/kyma-environment-broker/internal/dashboard"
	"github.com/kyma-project/control-plane/components/kyma-environment-broker/internal/middleware"
	"github.com/kyma-project/control-plane/components/kyma-environment-broker/internal/ptr"
	"github.com/kyma-project/control-plane/components/kyma-environment-broker/internal/storage"
	"github.com/kyma-project/control-plane/components/kyma-environment-broker/internal/storage/dberr"
	"github.com/pivotal-cf/brokerapi/v8/domain"
	"github.com/pivotal-cf/brokerapi/v8/domain/apiresponses"
	"github.com/sirupsen/logrus"
)

//go:generate mockery --name=Queue --output=automock --outpkg=automock --case=underscore
//go:generate mockery --name=PlanValidator --output=automock --outpkg=automock --case=underscore

type (
	Queue interface {
		Add(operationId string)
	}

	PlanValidator interface {
		IsPlanSupport(planID string) bool
	}
)

type ProvisionEndpoint struct {
	config            Config
	operationsStorage storage.Operations
	instanceStorage   storage.Instances
	queue             Queue
	builderFactory    PlanValidator
	enabledPlanIDs    map[string]struct{}
	plansConfig       PlansConfig
	kymaVerOnDemand   bool
	planDefaults      PlanDefaults

	shootDomain       string
	shootProject      string
	shootDnsProviders gardener.DNSProvidersData

	dashboardConfig dashboard.Config

	euAccessWhitelist        euaccess.WhitelistSet
	euAccessRejectionMessage string

	log logrus.FieldLogger
}

func NewProvision(cfg Config,
	gardenerConfig gardener.Config,
	operationsStorage storage.Operations,
	instanceStorage storage.Instances,
	queue Queue,
	builderFactory PlanValidator,
	plansConfig PlansConfig,
	kvod bool,
	planDefaults PlanDefaults,
	euAccessWhitelist euaccess.WhitelistSet,
	euRejectMessage string,
	log logrus.FieldLogger,
	dashboardConfig dashboard.Config,
) *ProvisionEndpoint {
	enabledPlanIDs := map[string]struct{}{}
	for _, planName := range cfg.EnablePlans {
		id := PlanIDsMapping[planName]
		enabledPlanIDs[id] = struct{}{}
	}

	return &ProvisionEndpoint{
		config:                   cfg,
		operationsStorage:        operationsStorage,
		instanceStorage:          instanceStorage,
		queue:                    queue,
		builderFactory:           builderFactory,
		log:                      log.WithField("service", "ProvisionEndpoint"),
		enabledPlanIDs:           enabledPlanIDs,
		plansConfig:              plansConfig,
		kymaVerOnDemand:          kvod,
		shootDomain:              gardenerConfig.ShootDomain,
		shootProject:             gardenerConfig.Project,
		shootDnsProviders:        gardenerConfig.DNSProviders,
		planDefaults:             planDefaults,
		euAccessWhitelist:        euAccessWhitelist,
		euAccessRejectionMessage: euRejectMessage,
		dashboardConfig:          dashboardConfig,
	}
}

// Provision creates a new service instance
//
//	PUT /v2/service_instances/{instance_id}
func (b *ProvisionEndpoint) Provision(ctx context.Context, instanceID string, details domain.ProvisionDetails, asyncAllowed bool) (domain.ProvisionedServiceSpec, error) {
	operationID := uuid.New().String()
	logger := b.log.WithFields(logrus.Fields{"instanceID": instanceID, "operationID": operationID, "planID": details.PlanID})
	logger.Infof("Provision called with context: %s", marshallRawContext(hideSensitiveDataFromRawContext(details.RawContext)))

	region, found := middleware.RegionFromContext(ctx)
	if !found {
		err := fmt.Errorf("No region specified in request.")
		return domain.ProvisionedServiceSpec{}, apiresponses.NewFailureResponse(err, http.StatusInternalServerError, "provisioning")
	}
	platformProvider, found := middleware.ProviderFromContext(ctx)
	if !found {
		err := fmt.Errorf("No region specified in request.")
		return domain.ProvisionedServiceSpec{}, apiresponses.NewFailureResponse(err, http.StatusInternalServerError, "provisioning")
	}

	// validation of incoming input
	ersContext, parameters, err := b.validateAndExtract(details, platformProvider, ctx, logger)
	if err != nil {
		errMsg := fmt.Sprintf("[instanceID: %s] %s", instanceID, err)
		return domain.ProvisionedServiceSpec{}, apiresponses.NewFailureResponse(err, http.StatusBadRequest, errMsg)
	}

	provisioningParameters := internal.ProvisioningParameters{
		PlanID:           details.PlanID,
		ServiceID:        details.ServiceID,
		ErsContext:       ersContext,
		Parameters:       parameters,
		PlatformRegion:   region,
		PlatformProvider: platformProvider,
	}

	logger.Infof("Starting provisioning runtime: Name=%s, GlobalAccountID=%s, SubAccountID=%s PlatformRegion=%s, ProvisioningParameterts.Region=%s, ProvisioningParameterts.MachineType=%s",
		parameters.Name, ersContext.GlobalAccountID, ersContext.SubAccountID, region, valueOfPtr(parameters.Region), valueOfPtr(parameters.MachineType))
	logParametersWithMaskedKubeconfig(parameters, logger)

	// check if operation with instance ID already created
	existingOperation, errStorage := b.operationsStorage.GetProvisioningOperationByInstanceID(instanceID)
	switch {
	case errStorage != nil && !dberr.IsNotFound(errStorage):
		logger.Errorf("cannot get existing operation from storage %s", errStorage)
		return domain.ProvisionedServiceSpec{}, fmt.Errorf("cannot get existing operation from storage")
	case existingOperation != nil && !dberr.IsNotFound(errStorage):
		return b.handleExistingOperation(existingOperation, provisioningParameters)
	}

	shootName := gardener.CreateShootName()
	shootDomainSuffix := strings.Trim(b.shootDomain, ".")

	dashboardURL := b.createDashboardURL(details.PlanID, instanceID)

	// create and save new operation
	operation, err := internal.NewProvisioningOperationWithID(operationID, instanceID, provisioningParameters)
	if err != nil {
		logger.Errorf("cannot create new operation: %s", err)
		return domain.ProvisionedServiceSpec{}, fmt.Errorf("cannot create new operation")
	}

	operation.ShootName = shootName
	operation.ShootDomain = fmt.Sprintf("%s.%s", shootName, shootDomainSuffix)
	operation.ShootDNSProviders = b.shootDnsProviders
	operation.DashboardURL = dashboardURL
	// for own cluster plan - KEB uses provided shoot name and shoot domain
	if IsOwnClusterPlan(provisioningParameters.PlanID) {
		operation.ShootName = provisioningParameters.Parameters.ShootName
		operation.ShootDomain = provisioningParameters.Parameters.ShootDomain
	}
	logger.Infof("Runtime ShootDomain: %s", operation.ShootDomain)

	err = b.operationsStorage.InsertOperation(operation.Operation)
	if err != nil {
		logger.Errorf("cannot save operation: %s", err)
		return domain.ProvisionedServiceSpec{}, fmt.Errorf("cannot save operation")
	}

	instance := internal.Instance{
		InstanceID:      instanceID,
		GlobalAccountID: ersContext.GlobalAccountID,
		SubAccountID:    ersContext.SubAccountID,
		ServiceID:       provisioningParameters.ServiceID,
		ServiceName:     KymaServiceName,
		ServicePlanID:   provisioningParameters.PlanID,
		ServicePlanName: PlanNamesMapping[provisioningParameters.PlanID],
		DashboardURL:    dashboardURL,
		Parameters:      operation.ProvisioningParameters,
	}
	err = b.instanceStorage.Insert(instance)
	if err != nil {
		logger.Errorf("cannot save instance in storage: %s", err)
		return domain.ProvisionedServiceSpec{}, fmt.Errorf("cannot save instance")
	}

	logger.Info("Adding operation to provisioning queue")
	b.queue.Add(operation.ID)

	return domain.ProvisionedServiceSpec{
		IsAsync:       true,
		OperationData: operation.ID,
		DashboardURL:  dashboardURL,
		Metadata: domain.InstanceMetadata{
			Labels: ResponseLabels(operation, instance, b.config.URL, b.config.EnableKubeconfigURLLabel),
		},
	}, nil
}

func logParametersWithMaskedKubeconfig(parameters internal.ProvisioningParametersDTO, logger *logrus.Entry) {
	parameters.Kubeconfig = "*****"
	logger.Infof("Runtime parameters: %+v", parameters)
}

func valueOfPtr(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

func (b *ProvisionEndpoint) validateAndExtract(details domain.ProvisionDetails, provider internal.CloudProvider, ctx context.Context, l logrus.FieldLogger) (internal.ERSContext, internal.ProvisioningParametersDTO, error) {
	var ersContext internal.ERSContext
	var parameters internal.ProvisioningParametersDTO

	if details.ServiceID != KymaServiceID {
		return ersContext, parameters, fmt.Errorf("service_id not recognized")
	}
	if _, exists := b.enabledPlanIDs[details.PlanID]; !exists {
		return ersContext, parameters, fmt.Errorf("plan ID %q is not recognized", details.PlanID)
	}

	ersContext, err := b.extractERSContext(details)
	logger := l.WithField("globalAccountID", ersContext.GlobalAccountID)
	if err != nil {
		return ersContext, parameters, fmt.Errorf("while extracting ers context: %w", err)
	}

	parameters, err = b.extractInputParameters(details)
	if err != nil {
		return ersContext, parameters, fmt.Errorf("while extracting input parameters: %w", err)
	}
	defaults, err := b.planDefaults(details.PlanID, provider, parameters.Provider)
	if err != nil {
		return ersContext, parameters, fmt.Errorf("while obtaining plan defaults: %w", err)
	}
	var autoscalerMin, autoscalerMax int
	if defaults.GardenerConfig != nil {
		p := defaults.GardenerConfig
		autoscalerMin, autoscalerMax = p.AutoScalerMin, p.AutoScalerMax
	}
	if err := parameters.AutoScalerParameters.Validate(autoscalerMin, autoscalerMax); err != nil {
		return ersContext, parameters, apiresponses.NewFailureResponse(err, http.StatusUnprocessableEntity, err.Error())
	}
	if parameters.OIDC.IsProvided() {
		if err := parameters.OIDC.Validate(); err != nil {
			return ersContext, parameters, apiresponses.NewFailureResponse(err, http.StatusUnprocessableEntity, err.Error())
		}
	}

	planValidator, err := b.validator(&details, provider,
		ctx)
	if err != nil {
		return ersContext, parameters, fmt.Errorf("while creating plan validator: %w", err)
	}
	result, err := planValidator.ValidateString(string(details.RawParameters))
	if err != nil {
		return ersContext, parameters, fmt.Errorf("while executing JSON schema validator: %w", err)
	}
	if !result.Valid {
		return ersContext, parameters, fmt.Errorf("while validating input parameters: %w", result.Error)
	}

	// EU Access: reject requests for not whitelisted globalAccountIds
	if isEuRestrictedAccess(ctx) {
		logger.Infof("EU Access restricted instance creation")
		if euaccess.IsNotWhitelisted(ersContext.GlobalAccountID, b.euAccessWhitelist) {
			logger.Infof(b.euAccessRejectionMessage)
			err = fmt.Errorf(b.euAccessRejectionMessage)
			return ersContext, parameters, apiresponses.NewFailureResponse(err, http.StatusBadRequest, "provisioning")
		}
	}

	if !b.kymaVerOnDemand {
		logger.Infof("Kyma on demand functionality is disabled. Default Kyma version will be used instead %s", parameters.KymaVersion)
		parameters.KymaVersion = ""
		parameters.OverridesVersion = ""
	}
	parameters.LicenceType = b.determineLicenceType(details.PlanID)

	found := b.builderFactory.IsPlanSupport(details.PlanID)
	if !found {
		return ersContext, parameters, fmt.Errorf("the plan ID not known, planID: %s", details.PlanID)
	}

	if IsOwnClusterPlan(details.PlanID) {
		decodedKubeconfig, err := base64.StdEncoding.DecodeString(parameters.Kubeconfig)
		if err != nil {
			return ersContext, parameters, fmt.Errorf("while decoding kubeconfig: %w", err)
		}
		parameters.Kubeconfig = string(decodedKubeconfig)
		err = validateKubeconfig(parameters.Kubeconfig)
		if err != nil {
			return ersContext, parameters, fmt.Errorf("while validating kubeconfig: %w", err)
		}
	}

	if IsTrialPlan(details.PlanID) && parameters.Region != nil && *parameters.Region != "" {
		_, valid := validRegionsForTrial[TrialCloudRegion(*parameters.Region)]
		if !valid {
			return ersContext, parameters, fmt.Errorf("invalid region specified in request for trial")
		}
	}

	if IsTrialPlan(details.PlanID) && b.config.OnlySingleTrialPerGA {
		count, err := b.instanceStorage.GetNumberOfInstancesForGlobalAccountID(ersContext.GlobalAccountID)
		if err != nil {
			return ersContext, parameters, fmt.Errorf("while checking if a trial Kyma instance exists for given global account: %w", err)
		}

		if count > 0 {
			logger.Info("Provisioning Trial SKR rejected, such instance was already created for this Global Account")
			return ersContext, parameters, fmt.Errorf("trial Kyma was created for the global account, but there is only one allowed")
		}
	}

	return ersContext, parameters, nil
}

func isEuRestrictedAccess(ctx context.Context) bool {
	platformRegion, _ := middleware.RegionFromContext(ctx)
	return euaccess.IsEURestrictedAccess(platformRegion)
}

// Rudimentary kubeconfig validation
func validateKubeconfig(kubeconfig string) error {
	config, err := clientcmd.Load([]byte(kubeconfig))
	if err != nil {
		return err
	}
	err = clientcmd.Validate(*config)
	if err != nil {
		return err
	}
	return nil
}

func (b *ProvisionEndpoint) extractERSContext(details domain.ProvisionDetails) (internal.ERSContext, error) {
	var ersContext internal.ERSContext
	err := json.Unmarshal(details.RawContext, &ersContext)
	if err != nil {
		return ersContext, fmt.Errorf("while decoding context: %w", err)
	}

	if ersContext.GlobalAccountID == "" {
		return ersContext, fmt.Errorf("global accountID parameter cannot be empty")
	}
	if ersContext.SubAccountID == "" {
		return ersContext, fmt.Errorf("subAccountID parameter cannot be empty")
	}
	if ersContext.UserID == "" {
		return ersContext, fmt.Errorf("UserID parameter cannot be empty")
	}
	ersContext.UserID = strings.ToLower(ersContext.UserID)

	return ersContext, nil
}

func (b *ProvisionEndpoint) extractInputParameters(details domain.ProvisionDetails) (internal.ProvisioningParametersDTO, error) {
	var parameters internal.ProvisioningParametersDTO
	err := json.Unmarshal(details.RawParameters, &parameters)
	if err != nil {
		return parameters, fmt.Errorf("while unmarshaling raw parameters: %w", err)
	}

	return parameters, nil
}

func (b *ProvisionEndpoint) handleExistingOperation(operation *internal.ProvisioningOperation, input internal.ProvisioningParameters) (domain.ProvisionedServiceSpec, error) {

	if !operation.ProvisioningParameters.IsEqual(input) {
		err := fmt.Errorf("provisioning operation already exist")
		msg := fmt.Sprintf("provisioning operation with InstanceID %s already exist", operation.InstanceID)
		return domain.ProvisionedServiceSpec{}, apiresponses.NewFailureResponse(err, http.StatusConflict, msg)
	}

	instance, err := b.instanceStorage.GetByID(operation.InstanceID)
	if err != nil {
		err := fmt.Errorf("cannot fetch instance for operation")
		msg := fmt.Sprintf("cannot fetch instance with ID: %s for operation woth ID: %s", operation.InstanceID, operation.ID)
		return domain.ProvisionedServiceSpec{}, apiresponses.NewFailureResponse(err, http.StatusConflict, msg)
	}

	return domain.ProvisionedServiceSpec{
		IsAsync:       true,
		OperationData: operation.ID,
		DashboardURL:  operation.DashboardURL,
		Metadata: domain.InstanceMetadata{
			Labels: ResponseLabels(*operation, *instance, b.config.URL, b.config.EnableKubeconfigURLLabel),
		},
	}, nil
}

func (b *ProvisionEndpoint) determineLicenceType(planId string) *string {
	if planId == AzureLitePlanID || IsTrialPlan(planId) {
		return ptr.String(internal.LicenceTypeLite)
	}

	return nil
}

func (b *ProvisionEndpoint) validator(details *domain.ProvisionDetails, provider internal.CloudProvider, ctx context.Context) (JSONSchemaValidator, error) {
	platformRegion, _ := middleware.RegionFromContext(ctx)
	plans := Plans(b.plansConfig, provider, b.config.IncludeAdditionalParamsInSchema, euaccess.IsEURestrictedAccess(platformRegion))
	plan := plans[details.PlanID]
	schema := string(Marshal(plan.Schemas.Instance.Create.Parameters))

	return jsonschema.NewValidatorFromStringSchema(schema)
}

func (b *ProvisionEndpoint) createDashboardURL(planID, instanceID string) string {
	if IsOwnClusterPlan(planID) {
		return b.dashboardConfig.LandscapeURL
	} else {
		return fmt.Sprintf("%s/?kubeconfigID=%s", b.dashboardConfig.LandscapeURL, instanceID)
	}
}
