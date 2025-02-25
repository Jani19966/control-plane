package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	coreV1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/control-plane/components/kyma-environment-broker/common/orchestration"
	"github.com/kyma-project/control-plane/components/kyma-environment-broker/common/orchestration/strategies"
	"github.com/kyma-project/control-plane/components/kyma-environment-broker/internal"
	kebError "github.com/kyma-project/control-plane/components/kyma-environment-broker/internal/error"
	"github.com/kyma-project/control-plane/components/kyma-environment-broker/internal/notification"
	"github.com/kyma-project/control-plane/components/kyma-environment-broker/internal/storage"
	"github.com/kyma-project/control-plane/components/kyma-environment-broker/internal/storage/dberr"
	"github.com/pivotal-cf/brokerapi/v8/domain"
	"github.com/sirupsen/logrus"
)

type OperationFactory interface {
	NewOperation(o internal.Orchestration, r orchestration.Runtime, i internal.Instance, state domain.LastOperationState) (orchestration.RuntimeOperation, error)
	ResumeOperations(orchestrationID string) ([]orchestration.RuntimeOperation, error)
	CancelOperations(orchestrationID string) error
	RetryOperations(operationIDs []string) ([]orchestration.RuntimeOperation, error)
}

type orchestrationManager struct {
	orchestrationStorage storage.Orchestrations
	operationStorage     storage.Operations
	instanceStorage      storage.Instances
	resolver             orchestration.RuntimeResolver
	factory              OperationFactory
	executor             orchestration.OperationExecutor
	log                  logrus.FieldLogger
	pollingInterval      time.Duration
	k8sClient            client.Client
	configNamespace      string
	configName           string
	kymaVersion          string
	kubernetesVersion    string
	bundleBuilder        notification.BundleBuilder
	speedFactor          int
}

const maintenancePolicyKeyName = "maintenancePolicy"
const maintenanceWindowFormat = "150405-0700"

func (m *orchestrationManager) SpeedUp(factor int) {
	m.speedFactor = factor
}

func (m *orchestrationManager) Execute(orchestrationID string) (time.Duration, error) {
	logger := m.log.WithField("orchestrationID", orchestrationID)
	m.log.Infof("Processing orchestration %s", orchestrationID)
	o, err := m.orchestrationStorage.GetByID(orchestrationID)
	if err != nil {
		if o == nil {
			m.log.Errorf("orchestration %s failed: %s", orchestrationID, err)
			return time.Minute, nil
		}
		return m.failOrchestration(o, fmt.Errorf("failed to get orchestration: %w", err))
	}

	maintenancePolicy, err := m.getMaintenancePolicy()
	if err != nil {
		m.log.Warnf("while getting maintenance policy: %s", err)
	}

	operations, err := m.resolveOperations(o, maintenancePolicy)
	if err != nil {
		return m.failOrchestration(o, fmt.Errorf("failed to resolve operations: %w", err))
	}

	err = m.orchestrationStorage.Update(*o)
	if err != nil {
		logger.Errorf("while updating orchestration: %v", err)
		return m.pollingInterval, nil
	}
	// do not perform any action if the orchestration is finished
	if o.IsFinished() {
		m.log.Infof("Orchestration was already finished, state: %s", o.State)
		return 0, nil
	}

	strategy := m.resolveStrategy(o.Parameters.Strategy.Type, m.executor, logger)

	// ctreate notification after orchestration resolved
	if !m.bundleBuilder.DisabledCheck() {
		err := m.sendNotificationCreate(o, operations)
		//currently notification error can only be temporary error
		if err != nil && kebError.IsTemporaryError(err) {
			return 5 * time.Second, nil
		}
	}

	execID, err := strategy.Execute(operations, o.Parameters.Strategy)
	if err != nil {
		return 0, fmt.Errorf("failed to execute strategy: %w", err)
	}

	o, err = m.waitForCompletion(o, strategy, execID, logger)
	if err != nil && kebError.IsTemporaryError(err) {
		return 5 * time.Second, nil
	} else if err != nil {
		return 0, fmt.Errorf("while waiting for orchestration to finish: %w", err)
	}

	o.UpdatedAt = time.Now()
	err = m.orchestrationStorage.Update(*o)
	if err != nil {
		logger.Errorf("while updating orchestration: %v", err)
		return m.pollingInterval, nil
	}

	logger.Infof("Finished processing orchestration, state: %s", o.State)
	return 0, nil
}

func (m *orchestrationManager) getMaintenancePolicy() (orchestration.MaintenancePolicy, error) {
	policy := orchestration.MaintenancePolicy{}
	config := &coreV1.ConfigMap{}
	key := client.ObjectKey{Namespace: m.configNamespace, Name: m.configName}
	if err := m.k8sClient.Get(context.Background(), key, config); err != nil {
		return policy, fmt.Errorf("orchestration config is absent")
	}

	if config.Data[maintenancePolicyKeyName] == "" {
		return policy, fmt.Errorf("maintenance policy is absent from orchestration config")
	}

	err := json.Unmarshal([]byte(config.Data[maintenancePolicyKeyName]), &policy)
	if err != nil {
		return policy, fmt.Errorf("failed to unmarshal the policy config")
	}

	return policy, nil
}

// result contains the operations which from `kcp o *** retry` and its label are retrying, runtimes from target parameter
func (m *orchestrationManager) extractRuntimes(o *internal.Orchestration, runtimes []orchestration.Runtime, result []orchestration.RuntimeOperation) []orchestration.Runtime {
	var fileterRuntimes []orchestration.Runtime
	if o.State == orchestration.Pending {
		fileterRuntimes = runtimes
	} else {
		// o.State = retrying / in progress
		for _, retryOp := range result {
			for _, r := range runtimes {
				if retryOp.Runtime.InstanceID == r.InstanceID {
					fileterRuntimes = append(fileterRuntimes, r)
					break
				}
			}
		}
	}
	return fileterRuntimes
}

func (m *orchestrationManager) NewOperationForPendingRetrying(o *internal.Orchestration, policy orchestration.MaintenancePolicy, retryRT []orchestration.RuntimeOperation, updateWindow bool) ([]orchestration.RuntimeOperation, *internal.Orchestration, int, error) {
	result := []orchestration.RuntimeOperation{}
	runtimes, err := m.resolver.Resolve(o.Parameters.Targets)
	if err != nil {
		return result, o, len(runtimes), fmt.Errorf("while resolving targets: %w", err)
	}

	fileterRuntimes := m.extractRuntimes(o, runtimes, retryRT)

	for _, r := range fileterRuntimes {
		if updateWindow {
			windowBegin := time.Time{}
			windowEnd := time.Time{}
			days := []string{}

			if o.State == orchestration.Pending && o.Parameters.Strategy.MaintenanceWindow {
				windowBegin, windowEnd, days = resolveMaintenanceWindowTime(r, policy, o.Parameters.Strategy.ScheduleTime)
			}
			if o.State == orchestration.Retrying && o.Parameters.RetryOperation.Immediate && o.Parameters.Strategy.MaintenanceWindow {
				windowBegin, windowEnd, days = resolveMaintenanceWindowTime(r, policy, o.Parameters.Strategy.ScheduleTime)
			}

			r.MaintenanceWindowBegin = windowBegin
			r.MaintenanceWindowEnd = windowEnd
			r.MaintenanceDays = days
		} else {
			if o.Parameters.RetryOperation.Immediate {
				r.MaintenanceWindowBegin = time.Time{}
				r.MaintenanceWindowEnd = time.Time{}
				r.MaintenanceDays = []string{}
			}
		}

		inst, err := m.instanceStorage.GetByID(r.InstanceID)
		if err != nil {
			return nil, o, len(runtimes), fmt.Errorf("while getting instance %s: %w", r.InstanceID, err)
		}

		op, err := m.factory.NewOperation(*o, r, *inst, orchestration.Pending)
		if err != nil {
			return nil, o, len(runtimes), fmt.Errorf("while creating new operation for runtime id %q: %w", r.RuntimeID, err)
		}

		result = append(result, op)

	}

	if o.Parameters.Kyma == nil || o.Parameters.Kyma.Version == "" {
		o.Parameters.Kyma = &orchestration.KymaParameters{Version: m.kymaVersion}
	}
	if o.Parameters.Kubernetes == nil || o.Parameters.Kubernetes.KubernetesVersion == "" {
		o.Parameters.Kubernetes = &orchestration.KubernetesParameters{KubernetesVersion: m.kubernetesVersion}
	}

	if len(fileterRuntimes) != 0 {
		o.State = orchestration.InProgress
	} else {
		o.State = orchestration.Succeeded
	}
	return result, o, len(fileterRuntimes), nil
}

func (m *orchestrationManager) resolveOperations(o *internal.Orchestration, policy orchestration.MaintenancePolicy) ([]orchestration.RuntimeOperation, error) {
	result := []orchestration.RuntimeOperation{}
	if o.State == orchestration.Pending {
		var err error
		var runtTimesNum int
		result, o, runtTimesNum, err = m.NewOperationForPendingRetrying(o, policy, result, true)
		if err != nil {
			return nil, fmt.Errorf("while creating new operation for pending: %w", err)
		}

		o.Description = fmt.Sprintf("Scheduled %d operations", runtTimesNum)
	} else if o.State == orchestration.Retrying {
		//check retry operation list, if empty return error
		if len(o.Parameters.RetryOperation.RetryOperations) == 0 {
			return nil, fmt.Errorf("while retrying operations: %w",
				fmt.Errorf("o.Parameters.RetryOperation.RetryOperations is empty"))
		}
		retryRuntimes, err := m.factory.RetryOperations(o.Parameters.RetryOperation.RetryOperations)
		if err != nil {
			return retryRuntimes, fmt.Errorf("while resolving retrying orchestration: %w", err)
		}

		var runtTimesNum int
		result, o, runtTimesNum, err = m.NewOperationForPendingRetrying(o, policy, retryRuntimes, true)

		if err != nil {
			return nil, fmt.Errorf("while NewOperationForPendingRetrying: %w", err)
		}

		o.Description = updateRetryingDescription(o.Description, fmt.Sprintf("retried %d operations", runtTimesNum))
		o.Parameters.RetryOperation.RetryOperations = nil
		o.Parameters.RetryOperation.Immediate = false
		m.log.Infof("Resuming %d operations for orchestration %s", len(result), o.OrchestrationID)
	} else {
		// Resume processing of not finished upgrade operations after restart
		var err error
		result, err = m.factory.ResumeOperations(o.OrchestrationID)
		if err != nil {
			return result, fmt.Errorf("while resuming operation: %w", err)
		}

		m.log.Infof("Resuming %d operations for orchestration %s", len(result), o.OrchestrationID)
	}

	return result, nil
}

func (m *orchestrationManager) resolveStrategy(sType orchestration.StrategyType, executor orchestration.OperationExecutor, log logrus.FieldLogger) orchestration.Strategy {
	switch sType {
	case orchestration.ParallelStrategy:
		s := strategies.NewParallelOrchestrationStrategy(executor, log, 0)
		if m.speedFactor != 0 {
			s.SpeedUp(m.speedFactor)
		}
		return s
	}
	return nil
}

// waitForCompletion waits until processing of given orchestration ends or if it's canceled
func (m *orchestrationManager) waitForCompletion(o *internal.Orchestration, strategy orchestration.Strategy, execID string, log logrus.FieldLogger) (*internal.Orchestration, error) {
	orchestrationID := o.OrchestrationID
	canceled := false
	var err error
	var stats map[string]int
	execIDs := []string{execID}

	err = wait.PollImmediateInfinite(m.pollingInterval, func() (bool, error) {
		// check if orchestration wasn't canceled
		o, err = m.orchestrationStorage.GetByID(orchestrationID)
		switch {
		case err == nil:
			if o.State == orchestration.Canceling {
				log.Info("Orchestration was canceled")
				canceled = true
			}
		case dberr.IsNotFound(err):
			log.Errorf("while getting orchestration: %v", err)
			return false, err
		default:
			log.Errorf("while getting orchestration: %v", err)
			return false, nil
		}
		s, err := m.operationStorage.GetOperationStatsForOrchestration(o.OrchestrationID)
		if err != nil {
			log.Errorf("while getting operations: %v", err)
			return false, nil
		}
		stats = s

		numberOfNotFinished := 0
		numberOfInProgress, found := stats[orchestration.InProgress]
		if found {
			numberOfNotFinished += numberOfInProgress
		}
		numberOfPending, found := stats[orchestration.Pending]
		if found {
			numberOfNotFinished += numberOfPending
		}
		numberOfRetrying, found := stats[orchestration.Retrying]
		if found {
			numberOfNotFinished += numberOfRetrying
		}

		if len(o.Parameters.RetryOperation.RetryOperations) > 0 {
			ops, err := m.factory.RetryOperations(o.Parameters.RetryOperation.RetryOperations)
			if err != nil {
				// don't block the polling and cancel signal
				log.Errorf("PollImmediateInfinite() while handling retrying operations: %v", err)
			}

			result, o, _, err := m.NewOperationForPendingRetrying(o, orchestration.MaintenancePolicy{}, ops, false)
			if err != nil {
				log.Errorf("PollImmediateInfinite() while new operation for retrying instanceid : %v", err)
			}

			err = strategy.Insert(execID, result, o.Parameters.Strategy)
			if err != nil {
				retryExecID, err := strategy.Execute(result, o.Parameters.Strategy)
				if err != nil {
					return false, fmt.Errorf("while executing upgrade strategy during retrying: %w", err)
				}
				execIDs = append(execIDs, retryExecID)
				execID = retryExecID
			}
			o.Description = updateRetryingDescription(o.Description, fmt.Sprintf("retried %d operations", len(o.Parameters.RetryOperation.RetryOperations)))
			o.Parameters.RetryOperation.RetryOperations = nil
			o.Parameters.RetryOperation.Immediate = false

			err = m.orchestrationStorage.Update(*o)
			if err != nil {
				log.Errorf("PollImmediateInfinite() while updating orchestration: %v", err)
				return false, nil
			}
			m.log.Infof("PollImmediateInfinite() while resuming %d operations for orchestration %s", len(result), o.OrchestrationID)
		}

		// don't wait for pending operations if orchestration was canceled
		if canceled {
			return numberOfInProgress == 0, nil
		} else {
			return numberOfNotFinished == 0, nil
		}
	})
	if err != nil {
		return nil, fmt.Errorf("while waiting for scheduled operations to finish: %w", err)
	}

	return m.resolveOrchestration(o, strategy, execIDs, stats)
}

func (m *orchestrationManager) resolveOrchestration(o *internal.Orchestration, strategy orchestration.Strategy, execIDs []string, stats map[string]int) (*internal.Orchestration, error) {
	if o.State == orchestration.Canceling {
		err := m.factory.CancelOperations(o.OrchestrationID)
		if err != nil {
			return nil, fmt.Errorf("while resolving canceled operations: %w", err)
		}
		for _, execID := range execIDs {
			strategy.Cancel(execID)
		}
		// Send customer notification for cancel
		if !m.bundleBuilder.DisabledCheck() {
			err := m.sendNotificationCancel(o)
			//currently notification error can only be temporary error
			if err != nil && kebError.IsTemporaryError(err) {
				return nil, err
			}
		}
		o.State = orchestration.Canceled
	} else {
		state := orchestration.Succeeded
		if stats[orchestration.Failed] > 0 {
			state = orchestration.Failed
		}
		o.State = state
	}
	return o, nil
}

// resolves the next exact maintenance window time for the runtime
func resolveMaintenanceWindowTime(r orchestration.Runtime, policy orchestration.MaintenancePolicy, after time.Time) (time.Time, time.Time, []string) {
	ruleMatched := false

	for _, p := range policy.Rules {
		if p.Match.Plan != "" {
			matched, err := regexp.MatchString(p.Match.Plan, r.Plan)
			if err != nil || !matched {
				continue
			}
		}

		if p.Match.GlobalAccountID != "" {
			matched, err := regexp.MatchString(p.Match.GlobalAccountID, r.GlobalAccountID)
			if err != nil || !matched {
				continue
			}
		}

		if p.Match.Region != "" {
			matched, err := regexp.MatchString(p.Match.Region, r.Region)
			if err != nil || !matched {
				continue
			}
		}

		// We have a rule match here, either by one or all of the rule match options. Let's override maintenance attributes.
		ruleMatched = true
		if len(p.Days) > 0 {
			r.MaintenanceDays = p.Days
		}
		if p.TimeBegin != "" {
			if maintenanceWindowBegin, err := time.Parse(maintenanceWindowFormat, p.TimeBegin); err == nil {
				r.MaintenanceWindowBegin = maintenanceWindowBegin
			}
		}
		if p.TimeEnd != "" {
			if maintenanceWindowEnd, err := time.Parse(maintenanceWindowFormat, p.TimeEnd); err == nil {
				r.MaintenanceWindowEnd = maintenanceWindowEnd
			}
		}
		break
	}

	// If non of the rules matched, try to apply the default rule
	if !ruleMatched {
		if len(policy.Default.Days) > 0 {
			r.MaintenanceDays = policy.Default.Days
		}
		if policy.Default.TimeBegin != "" {
			if maintenanceWindowBegin, err := time.Parse(maintenanceWindowFormat, policy.Default.TimeBegin); err == nil {
				r.MaintenanceWindowBegin = maintenanceWindowBegin
			}
		}
		if policy.Default.TimeEnd != "" {
			if maintenanceWindowEnd, err := time.Parse(maintenanceWindowFormat, policy.Default.TimeEnd); err == nil {
				r.MaintenanceWindowEnd = maintenanceWindowEnd
			}
		}
	}

	n := time.Now()
	// If 'after' is in the future, set it as timepoint for the maintenance window calculation
	if after.After(n) {
		n = after
	}
	availableDays := orchestration.ConvertSliceOfDaysToMap(r.MaintenanceDays)
	start := time.Date(n.Year(), n.Month(), n.Day(), r.MaintenanceWindowBegin.Hour(), r.MaintenanceWindowBegin.Minute(), r.MaintenanceWindowBegin.Second(), r.MaintenanceWindowBegin.Nanosecond(), r.MaintenanceWindowBegin.Location())
	end := time.Date(n.Year(), n.Month(), n.Day(), r.MaintenanceWindowEnd.Hour(), r.MaintenanceWindowEnd.Minute(), r.MaintenanceWindowEnd.Second(), r.MaintenanceWindowEnd.Nanosecond(), r.MaintenanceWindowEnd.Location())
	// Set start/end date to the first available day (including today)
	diff := orchestration.FirstAvailableDayDiff(n.Weekday(), availableDays)
	start = start.AddDate(0, 0, diff)
	end = end.AddDate(0, 0, diff)

	// if the window end slips through the next day, adjust the date accordingly
	if end.Before(start) || end.Equal(start) {
		end = end.AddDate(0, 0, 1)
	}

	// if time window has already passed we wait until next available day
	if start.Before(n) && end.Before(n) {
		diff := orchestration.NextAvailableDayDiff(n.Weekday(), availableDays)
		start = start.AddDate(0, 0, diff)
		end = end.AddDate(0, 0, diff)
	}

	return start, end, r.MaintenanceDays
}

func (m *orchestrationManager) failOrchestration(o *internal.Orchestration, err error) (time.Duration, error) {
	m.log.Errorf("orchestration %s failed: %s", o.OrchestrationID, err)
	return m.updateOrchestration(o, orchestration.Failed, err.Error()), nil
}

func (m *orchestrationManager) updateOrchestration(o *internal.Orchestration, state, description string) time.Duration {
	o.UpdatedAt = time.Now()
	o.State = state
	o.Description = description
	err := m.orchestrationStorage.Update(*o)
	if err != nil {
		if !dberr.IsNotFound(err) {
			m.log.Errorf("while updating orchestration: %v", err)
			return time.Minute
		}
	}
	return 0
}

func (m *orchestrationManager) sendNotificationCreate(o *internal.Orchestration, operations []orchestration.RuntimeOperation) error {
	if o.State == orchestration.InProgress {
		if o.Parameters.NotificationState == "" {
			m.log.Info("Initialize notification status")
			o.Parameters.NotificationState = orchestration.NotificationPending
		}
		//Skip sending create signal if notification already existed
		if o.Parameters.NotificationState == orchestration.NotificationPending {
			eventType := ""
			tenants := []notification.NotificationTenant{}
			if o.Type == orchestration.UpgradeKymaOrchestration {
				eventType = notification.KymaMaintenanceNumber
			} else if o.Type == orchestration.UpgradeClusterOrchestration {
				eventType = notification.KubernetesMaintenanceNumber
			}
			for _, op := range operations {
				startDate := ""
				endDate := ""
				if o.Parameters.Strategy.MaintenanceWindow {
					startDate = op.Runtime.MaintenanceWindowBegin.String()
					endDate = op.Runtime.MaintenanceWindowEnd.String()
				} else {
					startDate = time.Now().Format("2006-01-02 15:04:05")
				}
				tenant := notification.NotificationTenant{
					InstanceID: op.Runtime.InstanceID,
					StartDate:  startDate,
					EndDate:    endDate,
				}
				tenants = append(tenants, tenant)
			}
			notificationParams := notification.NotificationParams{
				OrchestrationID: o.OrchestrationID,
				EventType:       eventType,
				Tenants:         tenants,
			}
			m.log.Info("Start to create notification")
			notificationBundle, err := m.bundleBuilder.NewBundle(o.OrchestrationID, notificationParams)
			if err != nil {
				m.log.Errorf("%s: %s", "failed to create Notification Bundle", err)
				return err
			}
			err = notificationBundle.CreateNotificationEvent()
			if err != nil {
				m.log.Errorf("%s: %s", "cannot send notification", err)
				return err
			}
			m.log.Info("Creating notification succedded")
			o.Parameters.NotificationState = orchestration.NotificationCreated
		}
	}
	return nil
}

func (m *orchestrationManager) sendNotificationCancel(o *internal.Orchestration) error {
	if o.Parameters.NotificationState == orchestration.NotificationCreated {
		notificationParams := notification.NotificationParams{
			OrchestrationID: o.OrchestrationID,
		}
		m.log.Info("Start to cancel notification")
		notificationBundle, err := m.bundleBuilder.NewBundle(o.OrchestrationID, notificationParams)
		if err != nil {
			m.log.Errorf("%s: %s", "failed to create Notification Bundle", err)
			return err
		}
		err = notificationBundle.CancelNotificationEvent()
		if err != nil {
			m.log.Errorf("%s: %s", "cannot cancel notification", err)
			return err
		}
		m.log.Info("Cancelling notification succedded")
		o.Parameters.NotificationState = orchestration.NotificationCancelled
	}
	return nil
}

func updateRetryingDescription(desc string, newDesc string) string {
	if strings.Contains(desc, "retrying") {
		return strings.Replace(desc, "retrying", newDesc, -1)
	}

	return desc + ", " + newDesc
}
