package walker

import (
	"context"
	"path"

	"github.com/pkg/errors"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

// WalkPrepared overlaps bounded node decoding from already loaded tree plaintext
// with ordinary ordered callbacks. It performs the same backend loads as Walk.
func WalkPrepared(ctx context.Context, repo restic.BlobLoader, root restic.ID, visitor WalkVisitor) error {
	prepared := newListingPreparation(repo)
	defer prepared.Close()
	tree, closeTree, err := prepared.loadOwned(ctx, root)
	defer closeTree()
	err = visitor.ProcessNode(root, "/", nil, err)

	if err != nil {
		if err == ErrSkipNode {
			err = nil
		}
		return err
	}

	return walkPrepared(ctx, prepared, "/", root, tree, visitor)
}

// walkPrepared recursively traverses the tree, ignoring subtrees when the ID of the
// subtree is in ignoreTrees. If err is nil and ignore is true, the subtree ID
// will be added to ignoreTrees by walk.
func walkPrepared(ctx context.Context, prepared *listingPreparation, prefix string, parentTreeID restic.ID, tree data.TreeNodeIterator, visitor WalkVisitor) (err error) {
	for item := range tree {
		if item.Error != nil {
			return item.Error
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		node := item.Node

		p := path.Join(prefix, node.Name)

		if node.Type == data.NodeTypeInvalid {
			return errors.Errorf("node type is empty for node %q", node.Name)
		}

		if node.Type != data.NodeTypeDir {
			err := visitor.ProcessNode(parentTreeID, p, node, nil)
			if err != nil {
				if err == ErrSkipNode {
					// skip the remaining entries in this tree
					break
				}

				return err
			}

			continue
		}

		if node.Subtree == nil {
			return errors.Errorf("subtree for node %v in tree %v is nil", node.Name, p)
		}

		err := func() error {
			subtree, closeTree, loadErr := prepared.loadOwned(ctx, *node.Subtree)
			defer closeTree()
			visitErr := visitor.ProcessNode(parentTreeID, p, node, loadErr)
			if visitErr == ErrSkipNode {
				return nil
			}
			if visitErr != nil {
				return visitErr
			}
			return walkPrepared(ctx, prepared, p, *node.Subtree, subtree, visitor)
		}()
		if err != nil {
			return err
		}

	}

	if visitor.LeaveDir != nil {
		return visitor.LeaveDir(prefix)
	}

	return nil
}
