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

package ocdav

import (
	"net/http"
	"strings"
)

func (s *svc) handleOptions(w http.ResponseWriter, r *http.Request) {
	allow := "OPTIONS, LOCK, GET, HEAD, POST, DELETE, PROPPATCH, COPY,"
	allow += " MOVE, UNLOCK, PROPFIND, MKCOL, REPORT, SEARCH,"
	allow += " PUT" // TODO(jfd): only for files ... but we cannot create the full path without a user ... which we only have when credentials are sent

	isPublic := strings.Contains(r.Context().Value(ctxKeyBaseURI).(string), "public-files")

	w.Header().Set(HeaderContentType, "application/xml")
	w.Header().Set("Allow", allow)
	w.Header().Set("DAV", "1, 2")
	w.Header().Set("MS-Author-Via", "DAV")
	if !isPublic {
		w.Header().Add(HeaderAccessControlAllowHeaders, HeaderTusResumable)
		w.Header().Add(HeaderAccessControlExposeHeaders, strings.Join([]string{HeaderTusResumable, HeaderTusVersion, HeaderTusExtension}, ","))
		w.Header().Set(HeaderTusResumable, "1.0.0") // TODO(jfd): only for dirs?
		w.Header().Set(HeaderTusVersion, "1.0.0")
		w.Header().Set(HeaderTusExtension, "creation,creation-with-upload,checksum,expiration")
		w.Header().Set(HeaderTusChecksumAlgorithm, "md5,sha1,crc32")
	}
	w.WriteHeader(http.StatusNoContent)
}
