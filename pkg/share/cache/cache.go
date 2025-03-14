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

package cache

import (
	"time"

	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
)

// Warmup is the interface to implement cache warmup strategies.
type Warmup interface {
	GetResourceInfos() ([]*provider.ResourceInfo, error)
}

// ResourceInfoCache is the interface to implement caches for resource infos.
type ResourceInfoCache interface {
	Get(key string) (*provider.ResourceInfo, error)
	GetKeys(keys []string) ([]*provider.ResourceInfo, error)
	Set(key string, info *provider.ResourceInfo) error
	SetWithExpire(key string, info *provider.ResourceInfo, expiration time.Duration) error
}
