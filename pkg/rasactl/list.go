/*
Copyright © 2021 Rasa Technologies GmbH

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package rasactl

import (
	"fmt"

	"github.com/RasaHQ/rasactl/pkg/status"
	"github.com/RasaHQ/rasactl/pkg/types"
)

// List lists all deployments.
func (r *RasaCtl) List() error {
	data := [][]string{}
	header := []string{"Current", "Name", "Status", "Rasa production", "Rasa worker", "Enterprise", "Version"}
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

		stateData, err := r.KubernetesClient.ReadSecretWithState()
		if err != nil {
			r.Log.Info("Can't read a secret with state", "namespace", namespace, "error", err)
		}
		r.HelmClient.Configuration = &types.HelmConfigurationSpec{
			ReleaseName: string(stateData[types.StateSecretHelmReleaseName]),
		}
		r.HelmClient.Namespace = namespace
		r.KubernetesClient.Helm.ReleaseName = string(stateData[types.StateSecretHelmReleaseName])

		isRunning, err := r.KubernetesClient.IsRasaXRunning()
		if err != nil {
			return err
		}
		status := "Stopped"
		if isRunning {
			status = "Running"
		}

		current := ""
		if namespace == r.Namespace {
			current = "*"
		}

		url, err := r.GetRasaXURL()
		if err != nil {
			return err
		}
		r.initRasaXClient()
		r.RasaXClient.URL = url

		versionEndpoint, err := r.RasaXClient.GetVersionEndpoint()
		if err == nil {
			enterprise := "inactive"
			if versionEndpoint.Enterprise {
				enterprise = "active"
			}

			data = append(data, []string{current, namespace, status,
				versionEndpoint.Rasa.Production,
				versionEndpoint.Rasa.Worker,
				enterprise,
				versionEndpoint.RasaX,
			},
			)
		} else {
			data = append(data, []string{current, namespace, status,
				"0.0.0",
				string(stateData[types.StateSecretRasaWorkerVersion]),
				string(stateData[types.StateSecretEnterprise]),
				string(stateData[types.StateSecretRasaXVersion]),
			},
			)
		}
	}

	status.PrintTable(
		header,
		data,
	)
	return nil
}
