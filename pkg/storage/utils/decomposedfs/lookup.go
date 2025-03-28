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

package decomposedfs

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	"github.com/cs3org/reva/pkg/appctx"
	ctxpkg "github.com/cs3org/reva/pkg/ctx"
	"github.com/cs3org/reva/pkg/errtypes"
	"github.com/cs3org/reva/pkg/storage/utils/decomposedfs/node"
	"github.com/cs3org/reva/pkg/storage/utils/decomposedfs/options"
	"github.com/cs3org/reva/pkg/storage/utils/decomposedfs/xattrs"
	"github.com/cs3org/reva/pkg/storage/utils/templates"
	"github.com/pkg/xattr"
)

// Lookup implements transformations from filepath to node and back.
type Lookup struct {
	Options *options.Options
}

// NodeFromResource takes in a request path or request id and converts it to a Node.
func (lu *Lookup) NodeFromResource(ctx context.Context, ref *provider.Reference) (*node.Node, error) {
	if ref.ResourceId != nil {
		// check if a storage space reference is used
		// currently, the decomposed fs uses the root node id as the space id
		n, err := lu.NodeFromID(ctx, ref.ResourceId)
		if err != nil {
			return nil, err
		}
		// is this a relative reference?
		if ref.Path != "" {
			p := filepath.Clean(ref.Path)
			if p != "." {
				// walk the relative path
				n, err = lu.WalkPath(ctx, n, p, false, func(ctx context.Context, n *node.Node) error {
					return nil
				})
				if err != nil {
					return nil, err
				}
			}
		}
		return n, nil
	}

	if ref.Path != "" {
		return lu.NodeFromPath(ctx, ref.GetPath(), false)
	}

	// reference is invalid
	return nil, fmt.Errorf("invalid reference %+v. at least resource_id or path must be set", ref)
}

// NodeFromPath converts a filename into a Node.
func (lu *Lookup) NodeFromPath(ctx context.Context, fn string, followReferences bool) (*node.Node, error) {
	log := appctx.GetLogger(ctx)
	log.Debug().Interface("fn", fn).Msg("NodeFromPath()")

	root, err := lu.HomeOrRootNode(ctx)
	if err != nil {
		return nil, err
	}

	n := root
	// TODO collect permissions of the current user on every segment
	fn = filepath.Clean(fn)
	if fn != "/" && fn != "." {
		n, err = lu.WalkPath(ctx, n, fn, followReferences, func(ctx context.Context, n *node.Node) error {
			log.Debug().Interface("node", n).Msg("NodeFromPath() walk")
			if n.SpaceRoot != nil && n.SpaceRoot != root {
				root = n.SpaceRoot
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	n.SpaceRoot = root
	return n, nil
}

// NodeFromID returns the internal path for the id.
func (lu *Lookup) NodeFromID(ctx context.Context, id *provider.ResourceId) (n *node.Node, err error) {
	if id == nil || id.OpaqueId == "" {
		return nil, fmt.Errorf("invalid resource id %+v", id)
	}
	n, err = node.ReadNode(ctx, lu, id.OpaqueId)
	if err != nil {
		return nil, err
	}

	return n, n.FindStorageSpaceRoot()
}

// Path returns the path for node.
func (lu *Lookup) Path(ctx context.Context, n *node.Node) (p string, err error) {
	var root *node.Node
	if root, err = lu.HomeOrRootNode(ctx); err != nil {
		return
	}
	for n.ID != root.ID {
		p = filepath.Join(n.Name, p)
		if n, err = n.Parent(); err != nil {
			appctx.GetLogger(ctx).
				Error().Err(err).
				Str("path", p).
				Interface("node", n).
				Msg("Path()")
			return
		}
	}
	p = filepath.Join("/", p)
	return
}

// RootNode returns the root node of the storage.
func (lu *Lookup) RootNode(ctx context.Context) (*node.Node, error) {
	n := node.New("root", "", "", 0, "", nil, lu)
	n.Exists = true
	return n, nil
}

// HomeNode returns the home node of a user.
func (lu *Lookup) HomeNode(ctx context.Context) (node *node.Node, err error) {
	if !lu.Options.EnableHome {
		return nil, errtypes.NotSupported("Decomposedfs: home supported disabled")
	}

	if node, err = lu.RootNode(ctx); err != nil {
		return
	}
	node, err = lu.WalkPath(ctx, node, lu.mustGetUserLayout(ctx), false, nil)
	return
}

// WalkPath calls n.Child(segment) on every path segment in p starting at the node r.
// If a function f is given it will be executed for every segment node, but not the root node r.
// If followReferences is given the current visited reference node is replaced by the referenced node.
func (lu *Lookup) WalkPath(ctx context.Context, r *node.Node, p string, followReferences bool, f func(ctx context.Context, n *node.Node) error) (*node.Node, error) {
	segments := strings.Split(strings.Trim(p, "/"), "/")
	var err error
	for i := range segments {
		if r, err = r.Child(ctx, segments[i]); err != nil {
			return r, err
		}

		if followReferences {
			if attrBytes, err := xattr.Get(r.InternalPath(), xattrs.ReferenceAttr); err == nil {
				realNodeID := attrBytes
				ref, err := xattrs.ReferenceFromAttr(realNodeID)
				if err != nil {
					return nil, err
				}

				r, err = lu.NodeFromID(ctx, ref.ResourceId)
				if err != nil {
					return nil, err
				}
			}
		}
		if node.IsSpaceRoot(r) {
			r.SpaceRoot = r
		}

		if !r.Exists && i < len(segments)-1 {
			return r, errtypes.NotFound(segments[i])
		}
		if f != nil {
			if err = f(ctx, r); err != nil {
				return r, err
			}
		}
	}
	return r, nil
}

// HomeOrRootNode returns the users home node when home support is enabled.
// it returns the storages root node otherwise.
func (lu *Lookup) HomeOrRootNode(ctx context.Context) (node *node.Node, err error) {
	if lu.Options.EnableHome {
		return lu.HomeNode(ctx)
	}
	return lu.RootNode(ctx)
}

// InternalRoot returns the internal storage root directory.
func (lu *Lookup) InternalRoot() string {
	return lu.Options.Root
}

// InternalPath returns the internal path for a given ID.
func (lu *Lookup) InternalPath(id string) string {
	return filepath.Join(lu.Options.Root, "nodes", id)
}

func (lu *Lookup) mustGetUserLayout(ctx context.Context) string {
	u := ctxpkg.ContextMustGetUser(ctx)
	return templates.WithUser(u, lu.Options.UserLayout)
}

// ShareFolder returns the internal storage root directory.
func (lu *Lookup) ShareFolder() string {
	return lu.Options.ShareFolder
}
