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
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	types "github.com/cs3org/go-cs3apis/cs3/types/v1beta1"
	"github.com/cs3org/reva/pkg/appctx"
	ctxpkg "github.com/cs3org/reva/pkg/ctx"
	"github.com/cs3org/reva/pkg/errtypes"
	"github.com/cs3org/reva/pkg/storage/utils/decomposedfs/node"
	"github.com/cs3org/reva/pkg/storage/utils/decomposedfs/xattrs"
	"github.com/pkg/errors"
	"github.com/pkg/xattr"
)

// Recycle items are stored inside the node folder and start with the uuid of the deleted node.
// The `.T.` indicates it is a trash item and what follows is the timestamp of the deletion.
// The deleted file is kept in the same location/dir as the original node. This prevents deletes
// from triggering cross storage moves when the trash is accidentally stored on another partition,
// because the admin mounted a different partition there.
// TODO For an efficient listing of deleted nodes the ocis storages trash folder should have
// contain a directory with symlinks to trash files for every userid/"root"

// ListRecycle returns the list of available recycle items.
func (fs *Decomposedfs) ListRecycle(ctx context.Context, basePath, key, relativePath string) ([]*provider.RecycleItem, error) {
	log := appctx.GetLogger(ctx)

	items := make([]*provider.RecycleItem, 0)

	// TODO how do we check if the storage allows listing the recycle for the current user? check owner of the root of the storage?
	// use permissions ReadUserPermissions?
	if fs.o.EnableHome {
		if !node.OwnerPermissions().ListContainer {
			log.Debug().Msg("owner not allowed to list trash")
			return items, errtypes.PermissionDenied("owner not allowed to list trash")
		}
	} else {
		if !node.NoPermissions().ListContainer {
			log.Debug().Msg("default permissions prevent listing trash")
			return items, errtypes.PermissionDenied("default permissions prevent listing trash")
		}
	}

	if key == "" && relativePath == "/" {
		return fs.listTrashRoot(ctx)
	}

	trashRoot := fs.getRecycleRoot(ctx)
	f, err := os.Open(filepath.Join(trashRoot, key, relativePath))
	if err != nil {
		if os.IsNotExist(err) {
			return items, nil
		}
		return nil, errors.Wrapf(err, "tree: error listing %s", trashRoot)
	}
	defer f.Close()

	parentNode, err := os.Readlink(filepath.Join(trashRoot, key))
	if err != nil {
		log.Error().Err(err).Str("trashRoot", trashRoot).Msg("error reading trash link, skipping")
		return nil, err
	}

	if md, err := f.Stat(); err != nil {
		return nil, err
	} else if !md.IsDir() {
		// this is the case when we want to directly list a file in the trashbin
		item, err := fs.createTrashItem(ctx, parentNode, filepath.Dir(relativePath), filepath.Join(trashRoot, key, relativePath))
		if err != nil {
			return items, err
		}
		items = append(items, item)
		return items, err
	}

	names, err := f.Readdirnames(0)
	if err != nil {
		return nil, err
	}
	for i := range names {
		if item, err := fs.createTrashItem(ctx, parentNode, relativePath, filepath.Join(trashRoot, key, relativePath, names[i])); err == nil {
			items = append(items, item)
		}
	}
	return items, nil
}

func (fs *Decomposedfs) createTrashItem(ctx context.Context, parentNode, intermediatePath, itemPath string) (*provider.RecycleItem, error) {
	log := appctx.GetLogger(ctx)
	trashRoot := fs.getRecycleRoot(ctx)
	trashnode, err := os.Readlink(itemPath)
	if err != nil {
		log.Error().Err(err).Str("trashRoot", trashRoot).Msg("error reading trash link, skipping")
		return nil, err
	}
	parts := strings.SplitN(filepath.Base(parentNode), ".T.", 2)
	if len(parts) != 2 {
		log.Error().Str("trashRoot", trashRoot).Str("trashnode", trashnode).Interface("parts", parts).Msg("malformed trash link, skipping")
		return nil, errors.New("malformed trash link")
	}

	nodePath := fs.lu.InternalPath(filepath.Base(trashnode))
	md, err := os.Stat(nodePath)
	if err != nil {
		log.Error().Err(err).Str("trashRoot", trashRoot).Str("trashnode", trashnode).Msg("could not stat trash item, skipping")
		return nil, err
	}

	item := &provider.RecycleItem{
		Type: getResourceType(md.IsDir()),
		Size: uint64(md.Size()),
		Key:  path.Join(parts[0], intermediatePath, filepath.Base(itemPath)),
	}
	if deletionTime, err := time.Parse(time.RFC3339Nano, parts[1]); err == nil {
		item.DeletionTime = &types.Timestamp{
			Seconds: uint64(deletionTime.Unix()),
			// TODO nanos
		}
	} else {
		log.Error().Err(err).Str("trashRoot", trashRoot).Str("link", trashnode).Interface("parts", parts).Msg("could parse time format, ignoring")
	}

	// lookup origin path in extended attributes
	parentPath := fs.lu.InternalPath(filepath.Base(parentNode))
	if attrBytes, err := xattr.Get(parentPath, xattrs.TrashOriginAttr); err == nil {
		item.Ref = &provider.Reference{Path: filepath.Join(string(attrBytes), intermediatePath, filepath.Base(itemPath))}
	} else {
		log.Error().Err(err).Str("trashRoot", trashRoot).Str("link", trashnode).Msg("could not read origin path, skipping")
		return nil, err
	}
	// TODO filter results by permission ... on the original parent? or the trashed node?
	// if it were on the original parent it would be possible to see files that were trashed before the current user got access
	// so -> check the trash node itself
	// hmm listing trash currently lists the current users trash or the 'root' trash. from ocs only the home storage is queried for trash items.
	// for now we can only really check if the current user is the owner
	if attrBytes, err := xattr.Get(nodePath, xattrs.OwnerIDAttr); err == nil {
		if fs.o.EnableHome {
			u := ctxpkg.ContextMustGetUser(ctx)
			if u.Id.OpaqueId != string(attrBytes) {
				log.Warn().Str("trashRoot", trashRoot).Str("link", trashnode).Msg("trash item not owned by current user, skipping")
				// continue
				return nil, errors.New("trash item not owned by current user")
			}
		}
	} else {
		log.Error().Err(err).Str("trashRoot", trashRoot).Str("link", trashnode).Msg("could not read owner, skipping")
		return nil, err
	}

	return item, nil
}

func (fs *Decomposedfs) listTrashRoot(ctx context.Context) ([]*provider.RecycleItem, error) {
	log := appctx.GetLogger(ctx)
	items := make([]*provider.RecycleItem, 0)

	trashRoot := fs.getRecycleRoot(ctx)
	f, err := os.Open(trashRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return items, nil
		}
		return nil, errors.Wrap(err, "tree: error listing "+trashRoot)
	}
	defer f.Close()

	names, err := f.Readdirnames(0)
	if err != nil {
		return nil, err
	}

	for i := range names {
		trashnode, err := os.Readlink(filepath.Join(trashRoot, names[i]))
		if err != nil {
			log.Error().Err(err).Str("trashRoot", trashRoot).Str("name", names[i]).Msg("error reading trash link, skipping")
			continue
		}
		parts := strings.SplitN(filepath.Base(trashnode), ".T.", 2)
		if len(parts) != 2 {
			log.Error().Err(err).Str("trashRoot", trashRoot).Str("name", names[i]).Str("trashnode", trashnode).Interface("parts", parts).Msg("malformed trash link, skipping")
			continue
		}

		nodePath := fs.lu.InternalPath(filepath.Base(trashnode))
		md, err := os.Stat(nodePath)
		if err != nil {
			log.Error().Err(err).Str("trashRoot", trashRoot).Str("name", names[i]).Str("trashnode", trashnode). /*.Interface("parts", parts)*/ Msg("could not stat trash item, skipping")
			continue
		}

		item := &provider.RecycleItem{
			Type: getResourceType(md.IsDir()),
			Size: uint64(md.Size()),
			Key:  parts[0],
		}
		if deletionTime, err := time.Parse(time.RFC3339Nano, parts[1]); err == nil {
			item.DeletionTime = &types.Timestamp{
				Seconds: uint64(deletionTime.Unix()),
				// TODO nanos
			}
		} else {
			log.Error().Err(err).Str("trashRoot", trashRoot).Str("name", names[i]).Str("link", trashnode).Interface("parts", parts).Msg("could parse time format, ignoring")
		}

		// lookup origin path in extended attributes
		var attrBytes []byte
		if attrBytes, err = xattr.Get(nodePath, xattrs.TrashOriginAttr); err == nil {
			item.Ref = &provider.Reference{Path: string(attrBytes)}
		} else {
			log.Error().Err(err).Str("trashRoot", trashRoot).Str("name", names[i]).Str("link", trashnode).Msg("could not read origin path, skipping")
			continue
		}
		// TODO filter results by permission ... on the original parent? or the trashed node?
		// if it were on the original parent it would be possible to see files that were trashed before the current user got access
		// so -> check the trash node itself
		// hmm listing trash currently lists the current users trash or the 'root' trash. from ocs only the home storage is queried for trash items.
		// for now we can only really check if the current user is the owner
		if attrBytes, err = xattr.Get(nodePath, xattrs.OwnerIDAttr); err == nil {
			if fs.o.EnableHome {
				u := ctxpkg.ContextMustGetUser(ctx)
				if u.Id.OpaqueId != string(attrBytes) {
					log.Warn().Str("trashRoot", trashRoot).Str("name", names[i]).Str("link", trashnode).Msg("trash item not owned by current user, skipping")
					continue
				}
			}
		} else {
			log.Error().Err(err).Str("trashRoot", trashRoot).Str("name", names[i]).Str("link", trashnode).Msg("could not read owner, skipping")
			continue
		}

		items = append(items, item)
	}
	return items, nil
}

// RestoreRecycleItem restores the specified item.
func (fs *Decomposedfs) RestoreRecycleItem(ctx context.Context, basePath, key, relativePath string, restoreRef *provider.Reference) error {
	if restoreRef == nil {
		restoreRef = &provider.Reference{}
	}
	rn, p, restoreFunc, err := fs.tp.RestoreRecycleItemFunc(ctx, key, relativePath, restoreRef.Path)
	if err != nil {
		return err
	}

	// check permissions of deleted node
	ok, err := fs.p.HasPermission(ctx, rn, func(rp *provider.ResourcePermissions) bool {
		return rp.RestoreRecycleItem
	})
	switch {
	case err != nil:
		return errtypes.InternalError(err.Error())
	case !ok:
		return errtypes.PermissionDenied(key)
	}

	ps, err := fs.p.AssemblePermissions(ctx, p)
	if err != nil {
		return errtypes.InternalError(err.Error())
	}

	// share receiver cannot restore to a shared resource to which she does not have write permissions.
	if !ps.InitiateFileUpload {
		return errtypes.PermissionDenied(key)
	}

	// Run the restore func
	return restoreFunc()
}

// PurgeRecycleItem purges the specified item.
func (fs *Decomposedfs) PurgeRecycleItem(ctx context.Context, basePath, key, relativePath string) error {
	rn, purgeFunc, err := fs.tp.PurgeRecycleItemFunc(ctx, key, relativePath)
	if err != nil {
		return err
	}

	// check permissions of deleted node
	ok, err := fs.p.HasPermission(ctx, rn, func(rp *provider.ResourcePermissions) bool {
		return rp.PurgeRecycle
	})
	switch {
	case err != nil:
		return errtypes.InternalError(err.Error())
	case !ok:
		return errtypes.PermissionDenied(key)
	}

	// Run the purge func
	return purgeFunc()
}

// EmptyRecycle empties the trash.
func (fs *Decomposedfs) EmptyRecycle(ctx context.Context) error {
	u, ok := ctxpkg.ContextGetUser(ctx)
	// TODO what permission should we check? we could check the root node of the user? or the owner permissions on his home root node?
	// The current impl will wipe your own trash. or when no user provided the trash of 'root'
	if !ok {
		return os.RemoveAll(fs.getRecycleRoot(ctx))
	}

	// TODO use layout, see Tree.Delete() for problem
	return os.RemoveAll(filepath.Join(fs.o.Root, "trash", u.Id.OpaqueId))
}

func getResourceType(isDir bool) provider.ResourceType {
	if isDir {
		return provider.ResourceType_RESOURCE_TYPE_CONTAINER
	}
	return provider.ResourceType_RESOURCE_TYPE_FILE
}

func (fs *Decomposedfs) getRecycleRoot(ctx context.Context) string {
	if fs.o.EnableHome {
		u := ctxpkg.ContextMustGetUser(ctx)
		// TODO use layout, see Tree.Delete() for problem
		return filepath.Join(fs.o.Root, "trash", u.Id.OpaqueId)
	}
	return filepath.Join(fs.o.Root, "trash", "root")
}
