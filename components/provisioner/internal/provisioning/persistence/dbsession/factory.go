package dbsession

import (
	"time"

	"github.com/pkg/errors"

	dbr "github.com/gocraft/dbr/v2"
	"github.com/kyma-project/control-plane/components/provisioner/internal/model"
	"github.com/kyma-project/control-plane/components/provisioner/internal/persistence/dberrors"
)

//go:generate mockery -name=Factory
type Factory interface {
	NewReadSession() ReadSession
	NewWriteSession() WriteSession
	NewReadWriteSession() ReadWriteSession
	NewSessionWithinTransaction() (WriteSessionWithinTransaction, dberrors.Error)
}

//go:generate mockery -name=ReadSession
type ReadSession interface {
	GetCluster(runtimeID string) (model.Cluster, dberrors.Error)
	GetOperation(operationID string) (model.Operation, dberrors.Error)
	GetLastOperation(runtimeID string) (model.Operation, dberrors.Error)
	GetGardenerClusterByName(name string) (model.Cluster, dberrors.Error)
	GetTenant(runtimeID string) (string, dberrors.Error)
	ListInProgressOperations() ([]model.Operation, dberrors.Error)
	GetRuntimeUpgrade(operationId string) (model.RuntimeUpgrade, dberrors.Error)
	GetTenantForOperation(operationID string) (string, dberrors.Error)
	InProgressOperationsCount() (model.OperationsCount, dberrors.Error)
	//TODO:Remove after schema migration
	GetProviderSpecificConfigsByProvider(provider string) ([]ProviderData, dberrors.Error)
	GetUpdatedProviderSpecificConfigByID(id string) (string, dberrors.Error)
}

//go:generate mockery -name=WriteSession
type WriteSession interface {
	InsertCluster(cluster model.Cluster) dberrors.Error
	InsertGardenerConfig(config model.GardenerConfig) dberrors.Error
	UpdateGardenerClusterConfig(config model.GardenerConfig) dberrors.Error
	InsertAdministrators(clusterId string, administrators []string) dberrors.Error
	InsertKymaConfig(kymaConfig model.KymaConfig) dberrors.Error
	InsertOperation(operation model.Operation) dberrors.Error
	UpdateOperationState(operationID string, message string, state model.OperationState, endTime time.Time) dberrors.Error
	UpdateOperationLastError(operationID, msg, reason, component string) dberrors.Error
	TransitionOperation(operationID string, message string, stage model.OperationStage, transitionTime time.Time) dberrors.Error
	UpdateKubeconfig(runtimeID string, kubeconfig string) dberrors.Error
	SetActiveKymaConfig(runtimeID string, kymaConfigId string) dberrors.Error
	UpdateUpgradeState(operationID string, upgradeState model.UpgradeState) dberrors.Error
	DeleteCluster(runtimeID string) dberrors.Error
	MarkClusterAsDeleted(runtimeID string) dberrors.Error
	InsertRuntimeUpgrade(runtimeUpgrade model.RuntimeUpgrade) dberrors.Error
	FixShootProvisioningStage(message string, newStage model.OperationStage, transitionTime time.Time) dberrors.Error
	UpdateTenant(runtimeID string, tenant string) dberrors.Error
	//TODO:Remove after schema migration
	UpdateProviderSpecificConfig(id string, providerSpecificConfig string) dberrors.Error
	InsertRelease(artifacts model.Release) dberrors.Error
	UpdateKubernetesVersion(runtimeID string, version string) dberrors.Error
	UpdateShootNetworkingFilterDisabled(runtimeID string, shootNetworkingFilterDisabled *bool) dberrors.Error
}

//go:generate mockery -name=ReadWriteSession
type ReadWriteSession interface {
	ReadSession
	WriteSession
}

type Transaction interface {
	Commit() dberrors.Error
	RollbackUnlessCommitted()
}

//go:generate mockery -name=WriteSessionWithinTransaction
type WriteSessionWithinTransaction interface {
	WriteSession
	Transaction
}

type factory struct {
	connection *dbr.Connection
	encrypt    encryptFunc
	decrypt    decryptFunc
}

func NewFactory(connection *dbr.Connection, secretKey string) (Factory, error) {
	if len(secretKey) == 0 {
		return nil, errors.New("empty encryption key provided")
	}
	return &factory{
		connection: connection,
		encrypt:    newEncryptFunc([]byte(secretKey)),
		decrypt:    newDecryptFunc([]byte(secretKey)),
	}, nil
}

func (sf *factory) NewReadSession() ReadSession {
	return readSession{
		session: sf.connection.NewSession(nil),
		decrypt: sf.decrypt,
	}
}

func (sf *factory) NewWriteSession() WriteSession {
	return writeSession{
		session: sf.connection.NewSession(nil),
		encrypt: sf.encrypt,
	}
}

func (sf *factory) NewReadWriteSession() ReadWriteSession {
	session := sf.connection.NewSession(nil)
	return readWriteSession{
		readSession:  readSession{session: session, decrypt: sf.decrypt},
		writeSession: writeSession{session: session, encrypt: sf.encrypt},
	}
}

type readWriteSession struct {
	readSession
	writeSession
}

func (sf *factory) NewSessionWithinTransaction() (WriteSessionWithinTransaction, dberrors.Error) {
	dbSession := sf.connection.NewSession(nil)
	dbTransaction, err := dbSession.Begin()

	if err != nil {
		return nil, dberrors.Internal("Failed to start transaction: %s", err)
	}

	return writeSession{
		session:     dbSession,
		transaction: dbTransaction,
		encrypt:     sf.encrypt,
	}, nil
}
