package rasaxctl

import (
	"fmt"
	"os"

	"github.com/RasaHQ/rasaxctl/pkg/docker"
	"github.com/RasaHQ/rasaxctl/pkg/helm"
	"github.com/RasaHQ/rasaxctl/pkg/k8s"
	"github.com/RasaHQ/rasaxctl/pkg/logger"
	"github.com/RasaHQ/rasaxctl/pkg/rasax"
	"github.com/RasaHQ/rasaxctl/pkg/status"
	"github.com/RasaHQ/rasaxctl/pkg/types"
	"github.com/RasaHQ/rasaxctl/pkg/utils"
	"github.com/RasaHQ/rasaxctl/pkg/utils/cloud"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

type RasaXCTL struct {
	KubernetesClient *k8s.Kubernetes
	HelmClient       *helm.Helm
	RasaXClient      *rasax.RasaX
	DockerClient     *docker.Docker
	Log              logr.Logger
	Spinner          *status.SpinnerMessage
	Namespace        string
	isRasaXRunning   bool
	isRasaXDeployed  bool
	CloudProvider    *cloud.Provider
}

func (r *RasaXCTL) InitClients() error {
	r.Log = logger.New()
	r.Spinner = &status.SpinnerMessage{}
	r.Spinner.New()

	cloudProvider := &cloud.Provider{Log: r.Log}
	cloudProvider.New()
	r.CloudProvider = cloudProvider

	r.KubernetesClient = &k8s.Kubernetes{
		Namespace:     r.Namespace,
		Log:           r.Log,
		CloudProvider: r.CloudProvider,
	}
	if err := r.KubernetesClient.New(); err != nil {
		return err
	}

	r.HelmClient = &helm.Helm{
		Log:           r.Log,
		Namespace:     r.Namespace,
		Spinner:       r.Spinner,
		CloudProvider: r.CloudProvider,
	}
	if err := r.HelmClient.New(); err != nil {
		return err
	}
	r.HelmClient.KubernetesBackendType = r.KubernetesClient.BackendType

	r.DockerClient = &docker.Docker{
		Namespace: r.Namespace,
		Log:       r.Log,
		Spinner:   r.Spinner,
	}
	if err := r.DockerClient.New(); err != nil {
		return err
	}

	if err := r.GetKindControlPlaneNodeInfo(); err != nil {
		return err
	}

	return nil
}

func (r *RasaXCTL) CheckDeploymentStatus() (bool, bool, error) {
	// Check if a Rasa X deployment is already installed and running
	isRasaXDeployed, err := r.HelmClient.IsDeployed()
	if err != nil {
		return false, false, err
	}
	r.isRasaXDeployed = isRasaXDeployed

	isRasaXRunning, err := r.KubernetesClient.IsRasaXRunning()
	if err != nil {
		return false, false, err
	}

	r.isRasaXRunning = isRasaXRunning

	return isRasaXDeployed, isRasaXRunning, nil
}

func (r *RasaXCTL) Start() error {

	if err := utils.ValidateName(r.HelmClient.Namespace); err != nil {
		return err
	}

	if err := r.KubernetesClient.CreateNamespace(); err != nil {
		return err
	}

	if err := r.KubernetesClient.AddNamespaceLabel(); err != nil {
		return err
	}

	// Init Rasa X client
	r.initRasaXClient()

	if err := r.startOrInstall(); err != nil {
		return err
	}

	if err := r.GetAllHelmValues(); err != nil {
		return err
	}

	url, err := r.GetRasaXURL()
	if err != nil {
		return err
	}
	r.RasaXClient.URL = url

	token, err := r.GetRasaXToken()
	if err != nil {
		return err
	}
	r.RasaXClient.Token = token

	if err := r.checkDeploymentStatus(); err != nil {
		return err
	}

	return nil
}

func (r *RasaXCTL) Delete() error {
	force := viper.GetBool("force")
	prune := viper.GetBool("prune")
	r.Spinner.Message("Deleting Rasa X")

	state, err := r.KubernetesClient.ReadSecretWithState()
	if err != nil && !force {
		return err
	} else if err != nil && force {
		r.Log.Info("Can't read state secret", "error", err)
	}

	if !prune {
		if err := r.HelmClient.Uninstall(); err != nil && !force {
			return err
		} else if err != nil && force {
			r.Log.Info("Can't uninstall helm chart", "error", err)
		}

		msgDelSec := "Deleting secret with rasaxctl state"
		r.Spinner.Message(msgDelSec)
		r.Log.Info(msgDelSec)
		if err := r.KubernetesClient.DeleteSecretWithState(); err != nil && !force {
			return err
		} else if err != nil && force {
			r.Log.Info("Can't delete secret with state", "error", err)
		}

		if err := r.KubernetesClient.DeleteNamespaceLabel(); err != nil && !force {
			return err
		} else if err != nil && force {
			r.Log.Info("Can't delete label", "error", err)
		}
	}

	if r.DockerClient.Kind.ControlPlaneHost != "" && string(state["project-path"]) != "" || force {
		r.Spinner.Message("Deleting persistent volume")
		if err := r.KubernetesClient.DeleteVolume(); err != nil && !force {
			return err
		} else if err != nil && force {
			r.Log.Info("Can't delete persistent volume", "error", err)
		}

		r.Spinner.Message("Deleting a kind node")
		nodeName := fmt.Sprintf("kind-%s", r.Namespace)
		r.Log.Info("Deleting a kind node", "node", nodeName)
		if err := r.DockerClient.DeleteKindNode(nodeName); err != nil && !force {
			return err
		} else if err != nil && force {
			r.Log.Info("Can't delete a kind node", "node", nodeName, "error", err)
		}
		if err := r.KubernetesClient.DeleteNode(nodeName); err != nil && !force {
			return err
		} else if err != nil && force {
			r.Log.Info("Can't delete a Kubernetes node", "node", nodeName, "error", err)
		}
	}

	if r.KubernetesClient.BackendType == types.KubernetesBackendLocal && r.CloudProvider.Name == types.CloudProviderUnknown {
		host := fmt.Sprintf("%s.rasaxctl.local.io", r.Namespace)
		err := utils.DeleteHostToEtcHosts(host)
		if err != nil && !force {
			return err
		} else if err != nil && force {
			r.Log.Info("Can't delete host entry", "error", err)
		}
	}

	if prune {
		r.Log.Info("Deleting namespace", "namespace", r.Namespace)
		if err := r.KubernetesClient.DeleteNamespace(); err != nil && !force {
			return err
		} else if err != nil && force {
			r.Log.Info("Can't delete namespace", "namespace", r.Namespace, "error", err)
		}
	}

	r.Spinner.Message("Done!")
	r.Spinner.Stop()
	return nil
}

func (r *RasaXCTL) Stop() error {
	r.Spinner.Message("Stopping Rasa X")
	if err := r.KubernetesClient.ScaleDown(); err != nil {
		return err
	}

	state, err := r.KubernetesClient.ReadSecretWithState()
	if err != nil {
		return err
	}

	if r.DockerClient.Kind.ControlPlaneHost != "" && string(state["project-path"]) != "" {
		nodeName := fmt.Sprintf("kind-%s", r.Namespace)
		if err := r.DockerClient.StopKindNode(nodeName); err != nil {
			return err
		}
	}
	r.Spinner.Message(fmt.Sprintf("Rasa X for the %s project has been stopped", r.Namespace))
	r.Spinner.Stop()
	return nil
}

func (r *RasaXCTL) startOrInstall() error {
	projectPath := viper.GetString("project-path")
	// Install Rasa X
	if !r.isRasaXDeployed && !r.isRasaXRunning {
		if projectPath != "" {
			if r.DockerClient.Kind.ControlPlaneHost != "" {

				// check if the project path exists

				if path, err := os.Stat(projectPath); err != nil {
					if os.IsNotExist(err) {
						return err
					} else if !path.IsDir() {
						return errors.Errorf("The %s path can't point to a file, it has to be a directory", projectPath)
					}
					return err
				}
				r.DockerClient.ProjectPath = projectPath

				r.Spinner.Message("Creating and joining a kind node")
				if err := r.CreateAndJoinKindNode(); err != nil {
					return err
				}
				volume, err := r.KubernetesClient.CreateVolume(projectPath)
				if err != nil {
					return err
				}
				r.HelmClient.PVCName = volume

			} else {
				return errors.Errorf("It looks like you don't use kind as a current Kubernetes context, the project-path flag is supported only with kind.")
			}
		}

		if err := r.KubernetesClient.SaveSecretWithState(); err != nil {
			return err
		}

		r.Spinner.Message("Deploying Rasa X")
		if err := r.HelmClient.Install(); err != nil {
			return err
		}
	} else if !r.isRasaXRunning {
		state, err := r.KubernetesClient.ReadSecretWithState()
		if err != nil {
			return err
		}
		// Start Rasa X if deployments are scaled down to 0
		msg := "Starting Rasa X"
		r.Spinner.Message(msg)
		r.Log.Info(msg)

		if string(state["project-path"]) != "" {
			if r.DockerClient.Kind.ControlPlaneHost != "" {
				nodeName := fmt.Sprintf("kind-%s", r.Namespace)
				if err := r.DockerClient.StartKindNode(nodeName); err != nil {
					return err
				}
			}
		}
		// Set configuration used for starting a stopped project.
		r.HelmClient.Configuration.StartProject = true

		if err := r.HelmClient.Upgrade(); err != nil {
			return err
		}

		if err := r.KubernetesClient.ScaleUp(); err != nil {
			return err
		}
	}
	return nil
}

func (r *RasaXCTL) GetAllHelmValues() error {
	allValues, err := r.HelmClient.GetValues()
	if err != nil {
		return err
	}
	r.KubernetesClient.Helm.Values = allValues

	return nil
}

func (r *RasaXCTL) GetRasaXURL() (string, error) {
	url, err := r.KubernetesClient.GetRasaXURL()
	if err != nil {
		return url, err
	}
	r.Log.V(1).Info("Get Rasa X URL", "url", url)
	return url, nil
}

func (r *RasaXCTL) GetRasaXToken() (string, error) {
	token, err := r.KubernetesClient.GetRasaXToken()
	if err != nil {
		return token, err
	}

	return token, nil
}

func (r *RasaXCTL) initRasaXClient() {
	r.RasaXClient = &rasax.RasaX{
		Log:            r.Log,
		SpinnerMessage: r.Spinner,
		WaitTimeout:    r.HelmClient.Configuration.Timeout,
	}
	r.RasaXClient.New()
}

func (r *RasaXCTL) checkDeploymentStatus() error {
	err := r.RasaXClient.WaitForRasaX()
	if err != nil {
		return err
	}

	r.Log.Info("Rasa X is ready", "url", r.RasaXClient.URL)
	r.Spinner.Message("Ready!")
	r.Spinner.Stop()

	rasaXVersion, err := r.RasaXClient.GetVersionEndpoint()
	if err != nil {
		return err
	}

	helmRelease, err := r.HelmClient.GetStatus()
	if err != nil {
		return err
	}

	if err := r.KubernetesClient.UpdateSecretWithState(rasaXVersion, helmRelease); err != nil {
		return err
	}

	if !r.isRasaXDeployed && !r.isRasaXRunning {
		// Print the status box only if it's a new Rasa X deployment
		status.PrintRasaXStatus(rasaXVersion, r.RasaXClient.URL)
	}
	return nil
}

func (r *RasaXCTL) Upgrade() error {

	if err := utils.ValidateName(r.HelmClient.Namespace); err != nil {
		return err
	}

	// Init Rasa X client
	r.initRasaXClient()

	r.Spinner.Message("Upgrading Rasa X")
	if err := r.HelmClient.Upgrade(); err != nil {
		return err
	}

	url, err := r.GetRasaXURL()
	if err != nil {
		return err
	}
	r.RasaXClient.URL = url

	if err := r.RasaXClient.WaitForRasaX(); err != nil {
		return err
	}

	rasaXVersion, err := r.RasaXClient.GetVersionEndpoint()
	if err != nil {
		return err
	}

	helmRelease, err := r.HelmClient.GetStatus()
	if err != nil {
		return err
	}

	if err := r.KubernetesClient.UpdateSecretWithState(rasaXVersion, helmRelease); err != nil {
		return err
	}

	return nil
}

func (r *RasaXCTL) List() error {
	data := [][]string{}
	namespaces, err := r.KubernetesClient.GetNamespaces()
	if err != nil {
		return err
	}

	if len(namespaces) == 0 {
		fmt.Println("Nothing to show, use the start command to create a new project")
		return nil
	}

	for _, namespace := range namespaces {
		r.KubernetesClient.Namespace = namespace
		isRunning, err := r.KubernetesClient.IsRasaXRunning()
		if err != nil {
			return err
		}
		status := "Stopped"
		if isRunning {
			status = "Running"
		}

		stateData, err := r.KubernetesClient.ReadSecretWithState()
		if err != nil {
			return err
		}

		data = append(data, []string{namespace, status,
			string(stateData[types.StateSecretRasaWorkerVersion]),
			string(stateData[types.StateSecretEnterprise]),
			string(stateData[types.StateSecretRasaXVersion]),
		},
		)
	}

	status.PrintTable(
		[]string{"Name", "Status", "Rasa worker", "Enterprise", "Version"},
		data,
	)
	return nil
}

func (r *RasaXCTL) Status() error {
	namespaces, err := r.KubernetesClient.GetNamespaces()
	if err != nil {
		return err
	}

	if len(namespaces) == 0 {
		fmt.Println("Nothing to show, use the start command to create a new project")
		return nil
	}

	isRunning, err := r.KubernetesClient.IsRasaXRunning()
	if err != nil {
		return err
	}
	statusProject := "Stopped"
	if isRunning {
		statusProject = "Running"
	}

	stateData, err := r.KubernetesClient.ReadSecretWithState()
	if err != nil {
		return err
	}

	fmt.Printf("Name: %s\n", r.Namespace)
	fmt.Printf("Status: %s\n", statusProject)
	fmt.Printf("Version: %s\n", stateData[types.StateSecretRasaXVersion])
	fmt.Printf("Rasa worker version: %s\n", stateData[types.StateSecretRasaWorkerVersion])

	projectPath := "not defined"
	if string(stateData[types.StateSecretProjectPath]) != "" {
		projectPath = string(stateData[types.StateSecretProjectPath])
	}
	fmt.Printf("Project path: %s\n", projectPath)

	if viper.GetBool("details") {
		r.HelmClient.Configuration = &types.HelmConfigurationSpec{
			ReleaseName: string(stateData[types.StateSecretHelmReleaseName]),
		}
		release, err := r.HelmClient.GetStatus()
		if err != nil {
			return err
		}
		fmt.Printf("Helm chart: %s-%s\n", release.Chart.Name(), release.Chart.Metadata.Version)
		fmt.Printf("Helm release: %s\n", release.Name)
		fmt.Printf("Helm release status: %s\n", release.Info.Status)

		fmt.Println()

		pods, err := r.KubernetesClient.GetPods()
		if err != nil {
			return err
		}

		data := [][]string{}
		for _, pod := range pods.Items {
			data = append(data,
				[]string{
					pod.Name,
					r.KubernetesClient.PodStatus(pod.Status.Conditions),
					string(pod.Status.Phase),
				},
			)
		}

		if len(pods.Items) != 0 {
			fmt.Print("Pod details:\n\n")
		}

		status.PrintTable(
			[]string{"Name", "Condition", "Status"},
			data,
		)
	}

	fmt.Println()

	return nil
}
