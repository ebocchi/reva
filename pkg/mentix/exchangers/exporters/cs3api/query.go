// Copyright 2018-2022 CERN
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// In applying this license, CERN does not waive the privileges and immunities
// granted to it by virtue of its status as an Intergovernmental Organization
// or submit itself to any jurisdiction.

package cs3api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	ocmprovider "github.com/cs3org/go-cs3apis/cs3/ocm/provider/v1beta1"
	"github.com/cs3org/reva/pkg/mentix/config"
	"github.com/cs3org/reva/pkg/mentix/meshdata"
	"github.com/cs3org/reva/pkg/mentix/utils"
	"github.com/rs/zerolog"
)

// HandleDefaultQuery processes a basic query.
func HandleDefaultQuery(meshData *meshdata.MeshData, params url.Values, conf *config.Configuration, log *zerolog.Logger) (int, []byte, error) {
	// Convert the mesh data
	ocmData, err := convertMeshDataToOCMData(meshData, conf.Exporters.CS3API.ElevatedServiceTypes, log)
	if err != nil {
		return http.StatusBadRequest, []byte{}, fmt.Errorf("unable to convert the mesh data to OCM data structures: %v", err)
	}

	// Marshal the OCM data as JSON
	data, err := json.MarshalIndent(ocmData, "", "\t")
	if err != nil {
		return http.StatusBadRequest, []byte{}, fmt.Errorf("unable to marshal the OCM data: %v", err)
	}

	return http.StatusOK, data, nil
}

func convertMeshDataToOCMData(meshData *meshdata.MeshData, elevatedServiceTypes []string, log *zerolog.Logger) ([]*ocmprovider.ProviderInfo, error) {
	// Convert the mesh data into the corresponding OCM data structures
	providers := make([]*ocmprovider.ProviderInfo, 0, len(meshData.Operators)*3)
	for _, op := range meshData.Operators {
		for _, site := range op.Sites {
			// Gather all services from the site
			services := make([]*ocmprovider.Service, 0, len(site.Services))

			addService := func(host string, endpoint *meshdata.ServiceEndpoint, addEndpoints []*ocmprovider.ServiceEndpoint, apiVersion string) {
				services = append(services, &ocmprovider.Service{
					Host:                normalizeHost(host, log),
					Endpoint:            convertServiceEndpointToOCMData(endpoint, log),
					AdditionalEndpoints: addEndpoints,
					ApiVersion:          apiVersion,
				})
			}

			for _, service := range site.Services {
				apiVersion := meshdata.GetPropertyValue(service.Properties, meshdata.PropertyAPIVersion, "")

				// Gather all additional endpoints of the service
				addEndpoints := make([]*ocmprovider.ServiceEndpoint, 0, len(service.AdditionalEndpoints))
				for _, endpoint := range service.AdditionalEndpoints {
					if utils.FindInStringArray(endpoint.Type.Name, elevatedServiceTypes, false) != -1 {
						endpointURL, _ := url.Parse(endpoint.URL)
						addService(endpointURL.Host, endpoint, nil, apiVersion)
					} else {
						addEndpoints = append(addEndpoints, convertServiceEndpointToOCMData(endpoint, log))
					}
				}

				addService(service.Host, service.ServiceEndpoint, addEndpoints, apiVersion)
			}

			// Copy the site info into a ProviderInfo
			provider := &ocmprovider.ProviderInfo{
				Name:         site.Name,
				FullName:     site.FullName,
				Description:  site.Description,
				Organization: site.Organization,
				Domain:       normalizeHost(site.Domain, log),
				Homepage:     site.Homepage,
				Email:        site.Email,
				Services:     services,
				Properties:   site.Properties,
			}
			provider.Properties[strings.ToUpper(meshdata.PropertyOperator)] = op.ID // Propagate the operator ID as a property
			providers = append(providers, provider)
		}
	}
	return providers, nil
}

func convertServiceEndpointToOCMData(endpoint *meshdata.ServiceEndpoint, log *zerolog.Logger) *ocmprovider.ServiceEndpoint {
	return &ocmprovider.ServiceEndpoint{
		Type: &ocmprovider.ServiceType{
			Name:        endpoint.Type.Name,
			Description: endpoint.Type.Description,
		},
		Name:        endpoint.Name,
		Path:        normalizeURLPath(endpoint.URL, log),
		IsMonitored: endpoint.IsMonitored,
		Properties:  endpoint.Properties,
	}
}
