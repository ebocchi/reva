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

package main

import (
	"encoding/gob"
	"io"
	"os"
	"time"

	rpc "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	collaboration "github.com/cs3org/go-cs3apis/cs3/sharing/collaboration/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	"github.com/jedib0t/go-pretty/table"
)

func shareListReceivedCommand() *command {
	cmd := newCommand("share-list-received")
	cmd.Description = func() string { return "list shares you have received" }
	cmd.Usage = func() string { return "Usage: share-list-received [-flags]" }
	cmd.Action = func(w ...io.Writer) error {
		ctx := getAuthContext()
		shareClient, err := getClient()
		if err != nil {
			return err
		}

		shareRequest := &collaboration.ListReceivedSharesRequest{}

		shareRes, err := shareClient.ListReceivedShares(ctx, shareRequest)
		if err != nil {
			return err
		}

		if shareRes.Status.Code != rpc.Code_CODE_OK {
			return formatError(shareRes.Status)
		}

		if len(w) == 0 {
			t := table.NewWriter()
			t.SetOutputMirror(os.Stdout)
			t.AppendHeader(table.Row{"#", "Owner.Idp", "Owner.OpaqueId", "ResourceId", "Permissions", "Type",
				"Grantee.Idp", "Grantee.OpaqueId", "Created", "Updated", "State"})
			for _, s := range shareRes.Shares {
				var idp, opaque string
				if s.Share.Grantee.Type == provider.GranteeType_GRANTEE_TYPE_USER {
					idp, opaque = s.Share.Grantee.GetUserId().Idp, s.Share.Grantee.GetUserId().OpaqueId
				} else if s.Share.Grantee.Type == provider.GranteeType_GRANTEE_TYPE_GROUP {
					idp, opaque = s.Share.Grantee.GetGroupId().Idp, s.Share.Grantee.GetGroupId().OpaqueId
				}
				t.AppendRows([]table.Row{
					{s.Share.Id.OpaqueId, s.Share.Owner.Idp, s.Share.Owner.OpaqueId, s.Share.ResourceId.String(),
						s.Share.Permissions.String(), s.Share.Grantee.Type.String(), idp,
						opaque, time.Unix(int64(s.Share.Ctime.Seconds), 0),
						time.Unix(int64(s.Share.Mtime.Seconds), 0), s.State.String()},
				})
			}
			t.Render()
		} else {
			enc := gob.NewEncoder(w[0])
			if err := enc.Encode(shareRes.Shares); err != nil {
				return err
			}
		}
		return nil
	}
	return cmd
}
