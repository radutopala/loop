package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/cdp"
	cdpdom "github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// maxScannedFrames caps how many out-of-process frames one listing reads: each
// costs a session attach, and a page full of ad frames has dozens.
const maxScannedFrames = 20

// frameScanTimeout bounds the scan of one out-of-process frame, attach
// included, so a frame whose renderer hangs costs only its own elements.
const frameScanTimeout = 3 * time.Second

// frameWorldName names the isolated world the scan runs in inside a frame.
// Chrome hands back the same world for the same name, so repeated listings
// don't pile up worlds.
const frameWorldName = "loop-element-scan"

// frameScanJS runs the page scan in a frame. A frame whose embedder can read
// it answers null: the embedder's own scan already walked it, and
// window.frameElement is visible to the frame exactly when that is so.
var frameScanJS = "(window.frameElement ? null : " + scanRefsJS + ")"

// frameRefs lists the interactive elements inside the frames the in-page scan
// cannot enter, cross-origin ones, placed in the page's viewport.
//
// Chrome keeps a cross-origin frame of the same site in the page's process,
// where an isolated world reaches it; a frame of another site runs as its own
// iframe target, which takes a session of its own. Either way the frame's
// rects are its own viewport's, so they move by the iframe's box, and those
// outside it, scrolled out of the frame, are left out: a click there would
// land on the page around it.
//
// It is best effort: a frame that cannot be read is logged and skipped, and
// the page's own refs answer regardless.
func (c *CDPClient) frameRefs() []ElementRef {
	var refs []ElementRef
	for _, id := range c.childFrameIDs() {
		found, err := c.scanInProcessFrame(id)
		if err != nil {
			c.logger.Debug("skipping frame in element scan", "frame_id", string(id), "error", err)
			continue
		}
		refs = append(refs, c.placeInPage(id, found)...)
	}
	targets := c.iframeTargetIDs()
	if len(targets) > maxScannedFrames {
		targets = targets[:maxScannedFrames]
	}
	for _, id := range targets {
		found, err := c.scanFrameTarget(id)
		if err != nil {
			c.logger.Debug("skipping frame in element scan", "frame_id", string(id), "error", err)
			continue
		}
		// An out-of-process frame's id is its target's.
		refs = append(refs, c.placeInPage(cdp.FrameID(id), found)...)
	}
	return refs
}

// frameTreeNode is the part of a Page.getFrameTree node the scan needs. Only
// the ids are decoded: the frames carry enums that newer Chrome extends.
type frameTreeNode struct {
	Frame struct {
		ID cdp.FrameID `json:"id"`
	} `json:"frame"`
	ChildFrames []frameTreeNode `json:"childFrames"`
}

// childFrameIDs lists every frame below the page's own in its process, in
// document order. Out-of-process frames are not in it.
func (c *CDPClient) childFrameIDs() []cdp.FrameID {
	var tree struct {
		FrameTree frameTreeNode `json:"frameTree"`
	}
	err := c.runFn(c.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return c.exec(ctx, string(page.CommandGetFrameTree), nil, &tree)
	}))
	if err != nil {
		c.logger.Debug("reading the frame tree for the element scan", "error", err)
		return nil
	}
	var ids []cdp.FrameID
	var walk func(nodes []frameTreeNode)
	walk = func(nodes []frameTreeNode) {
		for _, n := range nodes {
			ids = append(ids, n.Frame.ID)
			walk(n.ChildFrames)
		}
	}
	walk(tree.FrameTree.ChildFrames)
	return ids
}

// iframeTargetIDs lists the out-of-process frames of this page.
func (c *CDPClient) iframeTargetIDs() []target.ID {
	infos, err := c.targetsFunc(c.ctx)
	if err != nil {
		c.logger.Debug("listing frame targets for the element scan", "error", err)
		return nil
	}
	var ids []target.ID
	for _, info := range infos {
		if info.Type == "iframe" && info.ParentID == c.targetID {
			ids = append(ids, info.TargetID)
		}
	}
	return ids
}

// scanInProcessFrame scans a frame of the page's own process in an isolated
// world. It answers nil refs for a frame the page's scan already covered.
func (c *CDPClient) scanInProcessFrame(id cdp.FrameID) ([]ElementRef, error) {
	var refs []ElementRef
	err := c.runFn(c.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var world page.CreateIsolatedWorldReturns
		if err := c.exec(ctx, string(page.CommandCreateIsolatedWorld),
			page.CreateIsolatedWorld(id).WithWorldName(frameWorldName), &world); err != nil {
			return fmt.Errorf("creating isolated world: %w", err)
		}
		var err error
		refs, err = evalScan(ctx, c.exec, cdpruntime.Evaluate(frameScanJS).WithContextID(world.ExecutionContextID))
		return err
	}))
	return refs, err
}

// scanFrameTarget scans an out-of-process frame through a session of its own.
//
// The session is chromedp's, as only a context chromedp attached hears its
// replies, but it is detached by hand: cancelling a context chromedp attached
// closes its target, and closing an iframe target takes the frame out of the
// page.
func (c *CDPClient) scanFrameTarget(id target.ID) ([]ElementRef, error) {
	frameCtx, cancel := chromedp.NewContext(c.ctx, chromedp.WithTargetID(id))
	defer cancel()
	scanCtx, stop := context.WithTimeout(frameCtx, frameScanTimeout)
	defer stop()

	var refs []ElementRef
	err := c.runFn(scanCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		refs, err = evalScan(ctx, c.exec, cdpruntime.Evaluate(frameScanJS))
		return err
	}))

	if cc := chromedp.FromContext(frameCtx); cc.Target != nil {
		detachCtx, done := context.WithTimeout(c.ctx, time.Second)
		if derr := c.exec(cdp.WithExecutor(detachCtx, cc.Browser), string(target.CommandDetachFromTarget),
			target.DetachFromTarget().WithSessionID(cc.Target.SessionID), nil); derr != nil {
			c.logger.Debug("detaching from frame target", "frame_id", string(id), "error", derr)
		}
		done()
		cc.Target = nil
	}
	return refs, err
}

// evalScan evaluates a scan expression and decodes the refs it returns. A
// null answer is no refs.
func evalScan(ctx context.Context, exec cdpExecutor, params *cdpruntime.EvaluateParams) ([]ElementRef, error) {
	var res struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := exec(ctx, string(cdpruntime.CommandEvaluate),
		params.WithReturnByValue(true).WithAwaitPromise(false), &res); err != nil {
		return nil, err
	}
	if res.ExceptionDetails != nil {
		return nil, fmt.Errorf("scanning page: %s", res.ExceptionDetails.Text)
	}
	if len(res.Result.Value) == 0 {
		return nil, nil
	}
	var refs []ElementRef
	if err := json.Unmarshal(res.Result.Value, &refs); err != nil {
		return nil, err
	}
	return refs, nil
}

// placeInPage moves a frame's refs by its iframe's content box, keeping those
// whose centre falls inside it. Without the box none are kept: where they are
// on the page is unknown.
func (c *CDPClient) placeInPage(id cdp.FrameID, refs []ElementRef) []ElementRef {
	if len(refs) == 0 {
		return nil
	}
	var owner cdpdom.GetFrameOwnerReturns
	err := c.runFn(c.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return c.exec(ctx, string(cdpdom.CommandGetFrameOwner), cdpdom.GetFrameOwner(id), &owner)
	}))
	if err != nil {
		c.logger.Debug("finding the frame's iframe", "frame_id", string(id), "error", err)
		return nil
	}
	box, err := c.boxModelFunc(c.ctx, owner.BackendNodeID)
	if err != nil || box == nil || len(box.Content) < 8 {
		c.logger.Debug("measuring the frame's iframe", "frame_id", string(id), "error", err)
		return nil
	}
	left, top := box.Content[0], box.Content[1]
	right, bottom := box.Content[4], box.Content[5]
	var placed []ElementRef
	for _, ref := range refs {
		ref.X += left
		ref.Y += top
		cx, cy := ref.X+ref.Width/2, ref.Y+ref.Height/2
		if cx < left || cx > right || cy < top || cy > bottom {
			continue
		}
		placed = append(placed, ref)
	}
	return placed
}
