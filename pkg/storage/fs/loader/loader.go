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

package loader

import (
	// Load core storage filesystem backends.
	_ "github.com/cs3org/reva/pkg/storage/fs/cback"
	_ "github.com/cs3org/reva/pkg/storage/fs/cephfs"
	_ "github.com/cs3org/reva/pkg/storage/fs/eos"
	_ "github.com/cs3org/reva/pkg/storage/fs/eosgrpc"
	_ "github.com/cs3org/reva/pkg/storage/fs/eosgrpchome"
	_ "github.com/cs3org/reva/pkg/storage/fs/eoshome"
	_ "github.com/cs3org/reva/pkg/storage/fs/local"
	_ "github.com/cs3org/reva/pkg/storage/fs/localhome"
	_ "github.com/cs3org/reva/pkg/storage/fs/nextcloud"
	_ "github.com/cs3org/reva/pkg/storage/fs/ocis"
	_ "github.com/cs3org/reva/pkg/storage/fs/owncloud"
	_ "github.com/cs3org/reva/pkg/storage/fs/owncloudsql"
	_ "github.com/cs3org/reva/pkg/storage/fs/s3"
	_ "github.com/cs3org/reva/pkg/storage/fs/s3ng"
	// Add your own here.
)
