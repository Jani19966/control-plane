package runtime

import (
	"context"
	"time"

	"github.com/pkg/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/kyma-incubator/compass/components/director/pkg/graphql"

	"github.com/kyma-project/control-plane/components/provisioner/internal/apperrors"
	"github.com/kyma-project/control-plane/components/provisioner/internal/util"

	"github.com/kyma-project/control-plane/components/provisioner/internal/util/k8s"

	"github.com/kyma-project/control-plane/components/provisioner/internal/director"
	"github.com/kyma-project/control-plane/components/provisioner/internal/model"

	core "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AgentConfigurationSecretName         = "compass-agent-configuration"
	runtimeAgentComponentNameSpace       = "kyma-system"
	legacyRuntimeAgentComponentNameSpace = "compass-system"
)

//go:generate mockery -name=Configurator
type Configurator interface {
	ConfigureRuntime(cluster model.Cluster, kubeconfigRaw string) apperrors.AppError
}

type configurator struct {
	builder        k8s.K8sClientProvider
	directorClient director.DirectorClient
}

func NewRuntimeConfigurator(builder k8s.K8sClientProvider, directorClient director.DirectorClient) Configurator {
	return &configurator{
		builder:        builder,
		directorClient: directorClient,
	}
}

func (c *configurator) ConfigureRuntime(cluster model.Cluster, kubeconfigRaw string) apperrors.AppError {

	token, err := c.getConnectionToken(cluster)
	if err != nil {
		return err.Append("error getting one time token from Director")
	}

	// In order to meet Kyma modularisation requirements compass-system namespace will be removed at some point.
	// Please see the following issue for details: https://github.com/kyma-project/kyma/issues/15915
	// Provisioner must be able to configure Compass Runtime Agent no matter if it will be installed in compass-system or kyma-system namespace.
	// This solution creates redundant secret, however,it will work in both cases, and doesn't require Provisioner's API change.
	err = c.configureAgent(cluster, token, runtimeAgentComponentNameSpace, kubeconfigRaw)
	if err != nil {
		return err.Append("error configuring Runtime Agent")
	}

	err = c.configureAgent(cluster, token, legacyRuntimeAgentComponentNameSpace, kubeconfigRaw)
	if err != nil {
		return err.Append("error configuring Runtime Agent in legacy namespace")
	}

	return nil
}

func (c *configurator) configureAgent(cluster model.Cluster, token graphql.OneTimeTokenForRuntimeExt, namespace, kubeconfigRaw string) apperrors.AppError {
	var err apperrors.AppError

	k8sClient, err := c.builder.CreateK8SClient(kubeconfigRaw)
	if err != nil {
		return err.Append("error creating Config Map client")
	}

	configurationData := map[string]string{
		"CONNECTOR_URL": token.ConnectorURL,
		"RUNTIME_ID":    cluster.ID,
		"TENANT":        cluster.Tenant,
		"TOKEN":         token.Token,
	}

	err = util.RetryOnError(3*time.Second, 2, "Error while creating namespace for Runtime Agent configuration: %s", func() (err apperrors.AppError) {
		err = c.createNamespace(k8sClient.CoreV1().Namespaces(), namespace)
		return
	})

	if err != nil {
		return err.Append("error getting or creating namespace")
	}

	secret := &core.Secret{
		ObjectMeta: meta.ObjectMeta{
			Name:      AgentConfigurationSecretName,
			Namespace: namespace,
		},
		StringData: configurationData,
	}
	return c.upsertSecret(k8sClient.CoreV1().Secrets(namespace), secret)
}

func (c *configurator) getConnectionToken(cluster model.Cluster) (graphql.OneTimeTokenForRuntimeExt, apperrors.AppError) {
	var err apperrors.AppError
	var token graphql.OneTimeTokenForRuntimeExt

	err = util.RetryOnError(10*time.Second, 3, "Error while getting one time token from Director: %s", func() (err apperrors.AppError) {
		token, err = c.directorClient.GetConnectionToken(cluster.ID, cluster.Tenant)
		return
	})

	if err != nil {
		return graphql.OneTimeTokenForRuntimeExt{}, err
	}

	return token, nil
}

func (c *configurator) createNamespace(namespaceInterface v1.NamespaceInterface, namespace string) apperrors.AppError {
	ns := &core.Namespace{
		ObjectMeta: meta.ObjectMeta{Name: namespace},
	}
	_, err := namespaceInterface.Create(context.Background(), ns, meta.CreateOptions{})

	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return util.K8SErrorToAppError(errors.Wrap(err, "Failed to create namespace"))
	}
	return nil
}

func (c *configurator) upsertSecret(secretInterface v1.SecretInterface, secret *core.Secret) apperrors.AppError {
	_, err := secretInterface.Create(context.Background(), secret, meta.CreateOptions{})
	if err == nil {
		return nil
	}
	if !k8serrors.IsAlreadyExists(err) {
		return util.K8SErrorToAppError(err).Append("error creating Secret on Runtime")
	}

	_, err = secretInterface.Update(context.Background(), secret, meta.UpdateOptions{})
	if err != nil {
		return util.K8SErrorToAppError(err).Append("error updating Secret on Runtime")
	}
	return nil
}
