import { Device, RenderPass } from '@luma.gl/core';
import { GraphConfigInterface, GraphConfig } from './config';
import { GraphData } from './modules/GraphData';
import { GraphSimulation, PointPositionTexture, PositionTextureSource, PositionsReadOptions } from './simulation';
export declare class Graph implements PositionTextureSource {
    /** Current graph configuration. Always fully populated with default values for any unset properties. */
    config: GraphConfigInterface;
    /** The data model holding the ingested input arrays (owned by the simulation). */
    readonly graph: GraphData;
    /** Promise that resolves when the graph is fully initialized and ready to use */
    readonly ready: Promise<void>;
    /** Whether the graph has completed initialization */
    isReady: boolean;
    /**
     * The force simulation behind this graph. It owns the device, the data model,
     * the point positions and the forces. `Graph` adds rendering, the view and
     * user input on top of it.
     *
     * It is the same `GraphSimulation` an app can run on its own. Apps that embed
     * cosmos.gl use it to reach what `Graph` does not expose: the device, the
     * seeded random generator in `store`, and the data model.
     */
    readonly simulation: GraphSimulation;
    /** The graph's one tracker, following the set given to `trackPointPositionsByIndices`. */
    private tracker;
    /** Canvas element, assigned asynchronously during device initialization */
    private canvas;
    private attributionDivElement;
    private canvasD3Selection;
    private device;
    private requestAnimationFrameId;
    /**
     * Detects canvas element resizes:
     * resize → `_shouldSyncScreenSize` + `requestRender()` → `syncScreenSize()` next frame.
     * The work always runs in-frame, so resizing behaves the same whether the
     * loop was running or idle.
     */
    private resizeObserver;
    /** Set by the ResizeObserver; the next frame applies it via syncScreenSize(). */
    private _shouldSyncScreenSize;
    private isRightClickMouse;
    /**
     * Touch/pen long-press timer. Set on pointerdown for non-mouse pointers and
     * cancelled on pointerup, pointercancel, or movement past
     * LONG_PRESS_MOVE_THRESHOLD_PX. When it fires, the contextmenu callback chain
     * runs and the synthesized click is suppressed.
     */
    private _longPressTimerId;
    private _longPressStartX;
    private _longPressStartY;
    /**
     * Set when long-press fires contextmenu (or a browser-dispatched contextmenu
     * handles itself) so the synthesized click that follows the same touch is
     * dropped instead of routed to onPointClick/onBackgroundClick/onLinkClick.
     * Consumed by onClick; also cleared on next pointerdown.
     */
    private _shouldSuppressNextClick;
    /**
     * Namespaces the listeners this instance registers on `document`. With a
     * shared namespace, instances replace each other's handlers and one
     * instance's destroy() removes another's.
     */
    private readonly _instanceId;
    /** The simulation's store — shared so both halves observe one engine state. */
    private readonly store;
    /** The simulation's position engine — aliased for the render/interaction code. */
    private points;
    private lines;
    private readonly zoomInstance;
    private readonly transition;
    private readonly dragInstance;
    private fpsMonitor;
    private currentEvent;
    /**
     * The value of `_findHoveredItemExecutionCount` is incremented by 1 on each animation frame.
     * When the counter reaches MAX_HOVER_DETECTION_DELAY (default 4), it is reset to 0 and hover detection runs.
     */
    private _findHoveredItemExecutionCount;
    /**
     * If no pointer is over the Canvas, hover detection will not be executed.
     */
    private _isPointerOnCanvas;
    /**
     * Last mouse position for detecting significant mouse movement
     */
    private _lastMouseX;
    private _lastMouseY;
    /**
     * Last checked mouse position for hover detection
     */
    private _lastCheckedMouseX;
    private _lastCheckedMouseY;
    /**
     * Force hover detection on next frame, bypassing mouse movement check.
     * Set when scene changes but mouse stays still (after simulation or zoom ends).
     */
    private _shouldForceHoverDetection;
    /**
     * View transform the picking buffer was last validated against; a change
     * (zoom, resize) marks the buffer stale (see updatePickingBufferStaleness).
     */
    private _lastPickingMatrix;
    /**
     * After setting data and render graph at a first time, the fit logic will run
     * */
    private _isFirstRenderAfterInit;
    private _fitViewOnInitTimeoutID;
    private isPointColorUpdateNeeded;
    private isPointShapeUpdateNeeded;
    private isPointImageIndicesUpdateNeeded;
    private isLinksUpdateNeeded;
    private isLinkColorUpdateNeeded;
    private isLinkWidthUpdateNeeded;
    private isLinkArrowUpdateNeeded;
    private isLinkStyleUpdateNeeded;
    private isPointImageSizesUpdateNeeded;
    private _isDestroyed;
    /**
     * Simulation-only instance (constructed without a `div`): owns no canvas, installs
     * no input handlers, never schedules frames, and never clears or submits the device.
     */
    private readonly _isHeadless;
    /**
     * Create a new Graph instance.
     * @param div - Container element for the graph canvas. Pass `null` (or `undefined`)
     *   to create a **headless** simulation-only instance: no canvas is adopted or
     *   reparented, no zoom/drag/pointer/keyboard listeners are installed, no internal
     *   render loop runs, and an externally supplied device is never cleared, submitted,
     *   resized, or reparented. Drive the simulation with `step()` and read results via
     *   `getPointPositions*()` or `getPointPositionTexture()`. View-dependent APIs
     *   (zoom, fit, screen-space queries) are inert, and transitions snap instead of
     *   animating (nothing advances them without a render loop).
     * @param config - Optional configuration. Unset properties use default values.
     */
    constructor(div: HTMLDivElement | null | undefined, config?: GraphConfig, devicePromise?: Promise<Device>);
    /**
     * Returns the current simulation progress
     */
    get progress(): number;
    /**
     * A value that gives information about the running simulation status.
     */
    get isSimulationRunning(): boolean;
    /**
     * The maximum point size.
     * This value is the maximum size of the `gl.POINTS` primitive that WebGL can render on the user's hardware.
     */
    get maxPointSize(): number;
    /**
     * Apply a new configuration. Changes take effect immediately.
     *
     * **Important:** Every call fully resets the configuration to defaults first,
     * then applies the provided values on top. Properties not included in `config`
     * will revert to their default values — they are not preserved from the previous call.
     *
     * @param config - Configuration object. Only include the properties you want to set.
     */
    setConfig(config: GraphConfig): void;
    /**
     * Partially updates the graph configuration. Only the provided properties
     * will be changed; all other properties retain their current values.
     *
     * Properties set to `undefined` will be reset to their default values.
     *
     * @param config - A partial configuration object with the properties to update.
     */
    setConfigPartial(config: GraphConfig): void;
    /**
     * Sets the positions for the graph points.
     *
     * @param {Float32Array} pointPositions - A Float32Array representing the positions of points in the format [x1, y1, x2, y2, ..., xn, yn],
     * where `n` is the index of the point.
     * Example: `new Float32Array([1, 2, 3, 4, 5, 6])` sets the first point to (1, 2), the second point to (3, 4), and so on.
     * @param {boolean | undefined} dontRescale - For this call only, don't rescale the points.
     *   - `true`: Don't rescale.
     *   - `false` or `undefined` (default): Use the behavior defined by `config.rescalePositions`.
     * @note If `transitionDuration > 0` and the simulation is running, the simulation is automatically
     * paused for the transition and remains paused afterwards. Call `unpause()` to resume it.
     * @note A point whose position is `NaN` (either coordinate) is treated as **absent**: it fades out in place while every
     * other point keeps its index and on-screen position (no array compaction, no slide). Set a point to
     * `NaN` to remove it, and back to a real position to add one — both animate. By default an absent point
     * fades to nothing; pass per-point `setPointSizes` / `setPointColors` to customize the exit look.
     * Absent points are excluded from the force layout, so this is safe with the simulation on or off.
     * cosmos.gl does not auto-compact the gaps — drop them yourself and snap the renumber with
     * `render(undefined, 0)`.
     */
    setPointPositions(pointPositions: Float32Array, dontRescale?: boolean | undefined): void;
    /**
     * Sets the colors for the graph points.
     *
     * @param {Float32Array} pointColors - A Float32Array representing the colors of points in the format [r1, g1, b1, a1, r2, g2, b2, a2, ..., rn, gn, bn, an],
     * where each color is represented in RGBA format.
     * Example: `new Float32Array([1, 0, 0, 1, 0, 1, 0, 1])` sets the first point to red and the second point to green.
     * An array whose length does not match the point count is ignored: every channel then
     * resolves as `NaN` does (see the note). Pass an empty array to go back to the defaults.
     * @note A `NaN` channel resolves to the config default for a normal point, or to the exit default
     * (transparent) for an **absent** point (one whose position is `NaN`) — letting you control how a
     * removed point fades out (see `setPointPositions`).
    */
    setPointColors(pointColors: Float32Array): void;
    /**
     * Gets the current colors of the graph points, with `NaN` channels
     * resolved to the config default (or the exit default for an absent point).
     *
     * @returns {Float32Array} A **new** Float32Array (a snapshot — safe to mutate, computed on
     * each call) of point colors in the format [r1, g1, b1, a1, r2, g2, b2, a2, ..., rn, gn, bn, an],
     * where each color is in RGBA format. Returns an empty Float32Array if no point colors are set.
     */
    getPointColors(): Float32Array;
    /**
     * Sets the sizes for the graph points.
     *
     * @param {Float32Array} pointSizes - A Float32Array representing the sizes of points in the format [size1, size2, ..., sizen],
     * where `n` is the index of the point.
     * Example: `new Float32Array([10, 20, 30])` sets the first point to size 10, the second point to size 20, and the third point to size 30.
     * An array whose length does not match the point count is ignored: every size then
     * resolves as `NaN` does (see the note). Pass an empty array to go back to the defaults.
     * @note A `NaN` size resolves to the config default for a normal point, or to the exit default (`0`)
     * for an **absent** point (one whose position is `NaN`) — letting you control how a removed point
     * fades out (see `setPointPositions`).
     */
    setPointSizes(pointSizes: Float32Array): void;
    /**
     * Sets the shapes for the graph points.
     *
     * @param {Float32Array} pointShapes - A Float32Array representing the shapes of points in the format [shape1, shape2, ..., shapen],
     * where `n` is the index of the point and each shape value corresponds to a PointShape enum:
     * 0 = Circle, 1 = Square, 2 = Triangle, 3 = Diamond, 4 = Pentagon, 5 = Hexagon, 6 = Star, 7 = Cross, 8 = None.
     * Example: `new Float32Array([0, 1, 2])` sets the first point to Circle, the second point to Square, and the third point to Triangle.
     * Images are rendered above shapes.
     */
    setPointShapes(pointShapes: Float32Array): void;
    /**
     * Sets the images for the graph points using ImageData objects.
     * Images are rendered above shapes.
     * To use images, provide image indices via setPointImageIndices().
     * A list whose images all have zero width or height cannot be packed into an atlas: it is
     * rejected with a console warning and the previous images stay in use.
     *
     * @param {ImageData[]} imageDataArray - Array of ImageData objects to use as point images.
     * Example: `setImageData([imageData1, imageData2, imageData3])`
     */
    setImageData(imageDataArray: ImageData[]): void;
    /**
     * Sets which image each point should use from the images array.
     * Images are rendered above shapes.
     *
     * @param {Float32Array} imageIndices - A Float32Array representing which image each point uses in the format [index1, index2, ..., indexn],
     * where `n` is the index of the point and each value is an index into the images array provided to `setImageData`.
     * Example: `new Float32Array([0, 1, 0])` sets the first point to use image 0, second point to use image 1, third point to use image 0.
     */
    setPointImageIndices(imageIndices: Float32Array): void;
    /**
     * Sets the sizes for the point images.
     *
     * @param {Float32Array} imageSizes - A Float32Array representing the sizes of point images in the format [size1, size2, ..., sizen],
     * where `n` is the index of the point.
     * Example: `new Float32Array([10, 20, 30])` sets the first image to size 10, the second image to size 20, and the third image to size 30.
     * @note A size counts only for a point that draws an image (see `setPointImageIndices`); for any other point it is kept but ignored.
     */
    setPointImageSizes(imageSizes: Float32Array): void;
    /**
     * Gets the current sizes of the graph points, with `NaN` sizes
     * resolved to the config default (or the exit default for an absent point).
     * While point sizes transition, these are the targets; `getPointRadiusByIndex` moves with them.
     *
     * @returns {Float32Array} A **new** Float32Array (a snapshot — safe to mutate, computed on
     * each call) of point sizes in the format [size1, size2, ..., sizen],
     * where `n` is the index of the point. Returns an empty Float32Array if no point sizes are set.
     */
    getPointSizes(): Float32Array;
    /**
     * Sets the links for the graph.
     *
     * @param {Float32Array} links - A Float32Array representing the links between points
     * in the format [source1, target1, source2, target2, ..., sourcen, targetn],
     * where `source` and `target` are the indices of the points being linked.
     * Example: `new Float32Array([0, 1, 1, 2])` creates a link from point 0 to point 1 and another link from point 1 to point 2.
     */
    setLinks(links: Float32Array): void;
    /**
     * Sets the colors for the graph links.
     *
     * @param {Float32Array} linkColors - A Float32Array representing the colors of links in the format [r1, g1, b1, a1, r2, g2, b2, a2, ..., rn, gn, bn, an],
     * where each color is in RGBA format.
     * Example: `new Float32Array([1, 0, 0, 1, 0, 1, 0, 1])` sets the first link to red and the second link to green.
     * An array whose length does not match the link count is ignored: every link draws
     * `config.linkDefaultColor`. Pass an empty array to go back to the default.
     */
    setLinkColors(linkColors: Float32Array): void;
    /**
     * Gets the current colors of the graph links.
     *
     * @returns {Float32Array} A Float32Array representing the colors of links in the format [r1, g1, b1, a1, r2, g2, b2, a2, ..., rn, gn, bn, an],
     * where each color is in RGBA format. Returns an empty Float32Array if no link colors are set.
     */
    getLinkColors(): Float32Array;
    /**
     * Sets the widths for the graph links.
     *
     * @param {Float32Array} linkWidths - A Float32Array representing the widths of links in the format [width1, width2, ..., widthn],
     * where `n` is the index of the link.
     * Example: `new Float32Array([1, 2, 3])` sets the first link to width 1, the second link to width 2, and the third link to width 3.
     * An array whose length does not match the link count is ignored: every link draws
     * `config.linkDefaultWidth`. Pass an empty array to go back to the default.
     */
    setLinkWidths(linkWidths: Float32Array): void;
    /**
     * Gets the current widths of the graph links.
     * While link widths transition, these are the targets; `getLinkWidthByIndex` moves with them.
     *
     * @returns {Float32Array} A Float32Array representing the widths of links in the format [width1, width2, ..., widthn],
     * where `n` is the index of the link. Returns an empty Float32Array if no link widths are set.
     */
    getLinkWidths(): Float32Array;
    /**
     * Gets a link's width by its index, as drawn: while link widths transition, it moves with them.
     *
     * @param {number} index - The index of the link.
     * @returns {number | undefined} The width, or `undefined` for an index outside the links.
     */
    getLinkWidthByIndex(index: number): number | undefined;
    /**
     * Sets the arrows for the graph links.
     *
     * @param {boolean[]} linkArrows - An array of booleans indicating whether each link should have an arrow,
     * in the format [arrow1, arrow2, ..., arrown], where `n` is the index of the link.
     * Example: `[true, false, true]` sets arrows on the first and third links, but not on the second link.
     */
    setLinkArrows(linkArrows: boolean[]): void;
    /**
     * Sets the styles (stroke patterns) for the graph links.
     *
     * @param {Float32Array} linkStyles - A Float32Array representing the style of each link in the format [style1, style2, ..., stylen],
     * where `n` is the index of the link. Each value is a `LinkStyle` enum value: `0` = Solid, `1` = Dashed, `2` = Dotted.
     * Example: `new Float32Array([0, 1, 2])` sets the first link to solid, the second to dashed, and the third to dotted.
     * Invalid values fall back to the `linkDefaultStyle` config value.
     * Dash length and gap are controlled globally via the `linkDashLength` and `linkDashGap` config options.
     */
    setLinkStyles(linkStyles: Float32Array): void;
    /**
     * Gets the current styles of the graph links.
     *
     * @returns {Float32Array} A Float32Array representing the style of each link in the format [style1, style2, ..., stylen],
     * where `n` is the index of the link. Returns an empty Float32Array if no link styles are set.
     */
    getLinkStyles(): Float32Array;
    /**
     * Sets the strength for the graph links.
     *
     * @param {Float32Array} linkStrength - A Float32Array representing the strength of each link in the format [strength1, strength2, ..., strengthn],
     * where `n` is the index of the link.
     * Example: `new Float32Array([1, 2, 3])` sets the first link to strength 1, the second link to strength 2, and the third link to strength 3.
     */
    setLinkStrength(linkStrength: Float32Array): void;
    /**
     * Sets the point clusters for the graph.
     *
     * @param {(number | undefined)[]} pointClusters - Array of cluster indices for each point in the graph.
     *   - Index: Each index corresponds to a point.
     *   - Values: Integers starting from 0; `undefined` indicates that a point does not belong to any cluster and will not be affected by cluster forces.
     * @example
     *   `[0, 1, 0, 2, undefined, 1]` maps points to clusters: point 0 and 2 to cluster 0, point 1 to cluster 1, and point 3 to cluster 2.
     * Points 4 is unclustered.
     * @note Clusters without specified positions via `setClusterPositions` will be positioned at their centermass by default.
     */
    setPointClusters(pointClusters: (number | undefined)[]): void;
    /**
     * Sets the positions of the point clusters for the graph.
     *
     * @param {(number | undefined)[]} clusterPositions - Array of cluster positions.
     *   - Every two elements represent the x and y coordinates for a cluster position.
     *   - `undefined` means the cluster's position is not defined and will use centermass positioning instead.
     * @example
     *   `[10, 20, 30, 40, undefined, undefined]` places the first cluster at (10, 20) and the second at (30, 40);
     * the third cluster will be positioned at its centermass automatically.
     */
    setClusterPositions(clusterPositions: (number | undefined)[]): void;
    /**
     * Sets the force strength coefficients for clustering points in the graph.
     *
     * This method allows you to customize the forces acting on individual points during the clustering process.
     * The force coefficients determine the strength of the forces applied to each point.
     *
     * @param {Float32Array} clusterStrength - A Float32Array of force strength coefficients for each point in the format [coeff1, coeff2, ..., coeffn],
     * where `n` is the index of the point.
     * Example: `new Float32Array([1, 0.4, 0.3])` sets the force coefficient for point 0 to 1, point 1 to 0.4, and point 2 to 0.3.
     */
    setPointClusterStrength(clusterStrength: Float32Array): void;
    /**
     * Sets which points are pinned (fixed) in position.
     *
     * Pinned points:
     * - Do not move due to physics forces (gravity, repulsion, link forces, etc.)
     * - Still participate in force calculations (other nodes are attracted to/repelled by them)
     * - Can still be dragged by the user if `enableDrag` is true
     *
     * @param {number[] | null} pinnedIndices - Array of point indices to pin. Set to `[]` or `null` to unpin all points.
     * @example
     *   // Pin points 0 and 5
     *   graph.setPinnedPoints([0, 5])
     *
     *   // Unpin all points
     *   graph.setPinnedPoints([])
     *   graph.setPinnedPoints(null)
     */
    setPinnedPoints(pinnedIndices: number[] | null): void;
    /**
     * Pins or unpins a single point without replacing the whole pinned set —
     * a one-texel GPU write instead of the full rebuild `setPinnedPoints` performs.
     * Designed for interactive hosts that pin a point on drag start and unpin it
     * on drag end (pair with `setPointPosition` to move it while pinned; check
     * `isPointPinned` first to restore a deliberate pin on release).
     * See `setPinnedPoints` for what pinning means.
     *
     * @param index - The index of the point.
     * @param pinned - `true` to pin, `false` to unpin.
     * @note Each call clones the tracked pinned set to keep later full rebuilds
     * in sync — fine at gesture rate; to change many pins at once, use
     * `setPinnedPoints`.
     */
    setPinnedPoint(index: number, pinned: boolean): void;
    /**
     * Whether a point index is currently pinned. Lets a host that pins during a
     * drag gesture restore the point's deliberate pinned state on release.
     */
    isPointPinned(index: number): boolean;
    /**
     * Moves a single point to a new position with a one-texel GPU write — no
     * full-array upload, no `render()` round trip. Together with the batched
     * `setPointPositionsByIndices` this is the API for mapping host-driven drag
     * interactions onto a live simulation.
     *
     * The write targets the **live** simulation state, like an interactive drag:
     * it does not modify the input array passed to `setPointPositions`, so a later
     * full data update starts from the input positions again. While the simulation
     * is running, forces move the point on the next tick unless it is pinned
     * (`setPinnedPoint`). While positions animate after `setPointPositions`, the
     * point stays where the write put it and the other points finish their
     * animation. A `setPointPositions` applied by a later `render()` moves it to
     * the position in that array.
     *
     * @param index - The index of the point.
     * @param x - New X coordinate, in space coordinates (as returned by `getPointPositions`).
     * @param y - New Y coordinate, in space coordinates.
     * @note An **absent** point (NaN position — see `setPointPositions`) is not a valid
     * target: the call is a no-op. No-op before the first `render()` call as well.
     */
    setPointPosition(index: number, x: number, y: number): void;
    /**
     * Batched form of `setPointPosition`: moves a sparse set of points in one call.
     * @param indices - Indices of the points to move.
     * @param positions - New positions aligned with `indices`, flat `[x0, y0, x1, y1, …]`
     *   in space coordinates.
     * @note Intended for interactive updates of a few points per frame. Bulk position
     * changes should go through `setPointPositions()` + `render()`, which upload one
     * texture instead of one texel per point.
     */
    setPointPositionsByIndices(indices: number[], positions: number[] | Float32Array): void;
    /**
     * Applies pending data changes and renders the graph.
     * Does not start or stop the simulation — use start(), stop(), pause(), unpause() for that.
     * Two exceptions: the `simulationAlpha` argument sets the alpha when provided, and a position
     * transition started with a positive `transitionDuration` (or its `config.transitionDuration`
     * fallback) pauses a running simulation (see `setPointPositions`).
     *
     * @param {number} [simulationAlpha] - Optional alpha value to set.
     *   - If 0: Sets alpha to 0, simulation stops after one frame (graph becomes static).
     *   - If positive: Sets alpha to that value.
     *   - If undefined: Keeps current alpha value.
     * @param {number} [transitionDuration] - Duration in milliseconds for any transition started by
     *   this render (from a preceding `setPointPositions` / `setPointColors` / … update), for this call
     *   only. `0` snaps with no animation; `undefined` (default) uses `config.transitionDuration`.
     *   To snap without changing the alpha, pass `render(undefined, 0)`.
     */
    render(simulationAlpha?: number, transitionDuration?: number): void;
    /**
     * Center the view on a point and zoom in, by point index.
     * @param index The index of the point in the array of points.
     * @param duration Duration of the animation transition in milliseconds (`700` by default).
     * @param scale Scale value to zoom in or out (`3` by default).
     * @param canZoomOut Set to `false` to prevent zooming out from the point (`true` by default).
     * @param enableSimulation Whether to run the simulation during the zoom transition (`true` by default).
     * @note An **absent** point (removed via a `NaN` position — see `setPointPositions`) is not a
     * zoom target: the call is a no-op, same as hover and selection never landing on one.
     */
    zoomToPointByIndex(index: number, duration?: number, scale?: number, canZoomOut?: boolean, enableSimulation?: boolean): void;
    /**
     * Zoom the view in or out to the specified zoom level.
     * @param value Zoom level
     * @param duration Duration of the zoom in/out transition.
     * @param enableSimulation Whether to run the simulation during the zoom transition (`true` by default).
     */
    zoom(value: number, duration?: number, enableSimulation?: boolean): void;
    /**
     * Zoom the view in or out to the specified zoom level.
     * @param value Zoom level
     * @param duration Duration of the zoom in/out transition.
     * @param enableSimulation Whether to run the simulation during the zoom transition (`true` by default).
     */
    setZoomLevel(value: number, duration?: number, enableSimulation?: boolean): void;
    /**
     * Get zoom level.
     * @returns Zoom level value of the view.
     */
    getZoomLevel(): number;
    /**
     * Get current X and Y coordinates of the points.
     * @returns Array of point positions.
     * @note Reads the GPU synchronously — the call stalls until the GPU has drained
     * all pending work. For polling positions during a running simulation prefer
     * `getPointPositionsAsync()` (no stall) or, for GPU-side consumers,
     * `getPointPositionTexture()` (no readback at all).
     * @note An **absent** point (removed via a `NaN` position — see `setPointPositions`) reads back
     * as `NaN`, mirroring the input — not as its last on-screen coordinate. This keeps removal
     * detectable from the read-back and keeps absent points out of `fitView`.
     */
    getPointPositions(): number[];
    /**
     * Get current X and Y coordinates of the points as a `Float32Array` in
     * `[x0, y0, x1, y1, …]` order — same data as `getPointPositions()` without the
     * `number[]` conversion.
     * @param out - Optional destination array. Reused when it holds at least
     *   `2 × pointCount` values (extra tail values are left untouched); otherwise a
     *   new array is allocated and returned.
     * @returns The filled destination array (empty when there is nothing to read).
     * @note Reads the GPU synchronously — see `getPointPositions()`. Prefer
     * `getPointPositionsAsync()` while the simulation is running.
     */
    getPointPositionsArray(out?: Float32Array): Float32Array;
    /**
     * Asynchronous variant of `getPointPositionsArray()`: the pixel copy runs on the
     * GPU timeline and the promise resolves when it completes, so the CPU never
     * stalls waiting for in-flight simulation work.
     * @param out - Optional destination array, as in `getPointPositionsArray()`.
     * @returns Promise of the filled destination array (empty when there is nothing to read,
     *   or when a data rebuild changed the point count while the copy was in flight).
     */
    getPointPositionsAsync(out?: Float32Array): Promise<Float32Array>;
    /**
     * Get a read-only view of the GPU position texture, for hosts that sample point
     * positions directly on the GPU (e.g. a custom deck.gl layer) instead of reading
     * them back to the CPU. See `PointPositionTexture` for the texel layout and the
     * ping-pong/version contract.
     * @returns The texture view, or `undefined` before the first `render()` call.
     */
    getPointPositionTexture(): PointPositionTexture | undefined;
    /**
     * Get current X and Y coordinates of the clusters.
     * @param options Use `{ nonBlocking: true }` to take the latest positions the GPU has handed
     * back without stalling for the current ones.
     * @returns Array of cluster positions in `[x0, y0, x1, y1, ...]` order. Do not mutate the returned array.
     */
    getClusterPositions(options?: PositionsReadOptions): Readonly<number[]>;
    /**
     * Center and zoom in/out the view to fit all points in the scene.
     * @param duration Duration of the center and zoom in/out animation in milliseconds (`250` by default).
     * @param padding Padding around the viewport in percentage (`0.1` by default).
     * @param enableSimulation Whether to run the simulation during the zoom transition (`true` by default).
     */
    fitView(duration?: number, padding?: number, enableSimulation?: boolean): void;
    /**
     * Center and zoom in/out the view to fit points by their indices in the scene.
     * @param indices Point indices to fit in the view.
     * @param duration Duration of the center and zoom in/out animation in milliseconds (`250` by default).
     * @param padding Padding around the viewport in percentage (`0.1` by default).
     * @param enableSimulation Whether to run the simulation during the zoom transition (`true` by default).
     */
    fitViewByPointIndices(indices: number[], duration?: number, padding?: number, enableSimulation?: boolean): void;
    /**
     * Center and zoom in/out the view to fit points by their positions in the scene.
     * @param positions Flat array of point coordinates as `[x0, y0, x1, y1, ...]`.
     * @param duration Duration of the center and zoom in/out animation in milliseconds (`250` by default).
     * @param padding Padding around the viewport in percentage (`0.1` by default).
     * @param enableSimulation Whether to run the simulation during the zoom transition (`true` by default).
     */
    fitViewByPointPositions(positions: number[], duration?: number, padding?: number, enableSimulation?: boolean): void;
    /**
     * Sets the zoom transform so that the given point positions fit in the viewport, with optional animation.
     *
     * @param positions Flat array of point coordinates as `[x0, y0, x1, y1, ...]`.
     * @param duration Animation duration in milliseconds. Default `250`.
     * @param scale Optional scale factor; if omitted, scale is chosen to fit the positions.
     * @param padding Padding around the viewport as a fraction (e.g. `0.1` = 10%). Default `0.1`.
     * @param enableSimulation Whether to run the simulation during the zoom transition (`true` by default).
     */
    setZoomTransformByPointPositions(positions: Float32Array, duration?: number, scale?: number, padding?: number, enableSimulation?: boolean): void;
    /**
     * Find point indices inside a rectangular area.
     *
     * **Important:** This method is synchronous and must only be called when the graph is ready.
     * Ensure `await graph.ready` has resolved (or use the result inside `graph.ready.then(...)`) before
     * calling. If called before initialization completes, returns an empty array.
     *
     * @param rect - Array of two corner points `[[left, top], [right, bottom]]`.
     * The coordinates should be from 0 to the width/height of the canvas.
     * @returns Array of point indices inside the rectangle.
     */
    findPointsInRect(rect: [[number, number], [number, number]]): number[];
    /**
     * Find point indices inside a polygon area.
     *
     * **Important:** This method is synchronous and must only be called when the graph is ready.
     * Ensure `await graph.ready` has resolved (or use the result inside `graph.ready.then(...)`) before
     * calling. If called before initialization completes, returns an empty array.
     *
     * @param polygonPath - Array of points `[[x1, y1], [x2, y2], ..., [xn, yn]]` that defines the polygon.
     * The coordinates should be from 0 to the width/height of the canvas.
     * @returns Array of point indices inside the polygon.
     */
    findPointsInPolygon(polygonPath: [number, number][]): number[];
    /**
     * Get point indices that are neighbors of the given point(s) — connected by a link in either direction.
     * @param pointIndices A single point index or an array of point indices.
     * @returns Deduplicated array of neighboring point indices.
     */
    getNeighboringPointIndices(pointIndices: number | number[]): number[];
    /**
     * Get link indices where both endpoints are within the given point(s).
     * @param pointIndices A single point index or an array of point indices.
     * @returns Deduplicated array of link indices connecting points within the provided set.
     */
    getConnectedLinkIndices(pointIndices: number | number[]): number[];
    /**
     * Get point indices at the endpoints of the given link(s).
     * @param linkIndices A single link index or an array of link indices.
     * @returns Deduplicated array of point indices at the ends of the provided links.
     */
    getConnectedPointIndices(linkIndices: number | number[]): number[];
    /**
     * Converts the X and Y point coordinates from the space coordinate system to the screen coordinate system.
     * @param spacePosition Array of x and y coordinates in the space coordinate system.
     * @returns Array of x and y coordinates in the screen coordinate system.
     */
    spaceToScreenPosition(spacePosition: [number, number]): [number, number];
    /**
     * Converts the X and Y point coordinates from the screen coordinate system to the space coordinate system.
     * @param screenPosition Array of x and y coordinates in the screen coordinate system.
     * @returns Array of x and y coordinates in the space coordinate system.
     */
    screenToSpacePosition(screenPosition: [number, number]): [number, number];
    /**
     * Converts the point radius value from the space coordinate system to the screen coordinate system.
     * @param spaceRadius Radius of point in the space coordinate system.
     * @returns Radius of point in the screen coordinate system.
     */
    spaceToScreenRadius(spaceRadius: number): number;
    /**
     * Get point radius by its index, as drawn: the point size, or the image size when the
     * point draws an image and that is larger. While point sizes transition, it moves with them.
     * @param index Index of the point.
     * @returns Radius of the point.
     */
    getPointRadiusByIndex(index: number): number | undefined;
    /**
     * Track multiple point positions by their indices on each Cosmos tick.
     *
     * The tracked set is declarative: an index follows its point whenever that
     * point exists. Growing or shrinking the point count later keeps tracking
     * correct — an index past the current count simply reports nothing until the
     * count grows to include it. Tracking may also be set up before the first
     * `setPointPositions` call.
     * @param indices Array of points indices.
     */
    trackPointPositionsByIndices(indices: number[]): void;
    /**
     * Get current X and Y coordinates of the tracked points.
     * Do not mutate the returned map - it may affect future calls.
     * @param options Use `{ nonBlocking: true }` to take the latest positions the GPU has handed
     * back without stalling for the current ones.
     * @returns A ReadonlyMap where keys are point indices and values are their corresponding X and Y coordinates in the [number, number] format.
     * @see trackPointPositionsByIndices To set which points should be tracked
     * @note An **absent** tracked point (removed via a `NaN` position — see `setPointPositions`) is
     * omitted from the map — a missing key means "this point is gone". React to absence yourself
     * (e.g. hide its label); the entry disappears as soon as the point is removed, even while its
     * fade-out is still playing. A tracked index with no point behind it (at or past the current
     * point count) is omitted the same way, and reappears if the count grows to include it.
     */
    getTrackedPointPositionsMap(options?: PositionsReadOptions): ReadonlyMap<number, [number, number]>;
    /**
     * Get current X and Y coordinates of the tracked points as an array.
     * @returns Array of point positions in the format [x1, y1, x2, y2, ..., xn, yn] for tracked points only.
     * The positions are ordered by the tracking indices (same order as provided to trackPointPositionsByIndices).
     * Returns an empty array if no points are being tracked.
     * @note An **absent** tracked point (removed via a `NaN` position) reads back as `NaN` — the slot
     * is kept so positions stay aligned with the tracked indices.
     */
    getTrackedPointPositionsArray(): number[];
    /**
     * For the points that are currently visible on the screen, get a sample of point indices with their coordinates.
     * The resulting number of points will depend on the `pointSamplingDistance` configuration property,
     * and the sampled points will be evenly distributed.
     * @returns A Map object where keys are the index of the points and values are their corresponding X and Y coordinates in the [number, number] format.
     */
    getSampledPointPositionsMap(): Map<number, [number, number]>;
    /**
     * For the points that are currently visible on the screen, get a sample of point indices and positions.
     * The resulting number of points will depend on the `pointSamplingDistance` configuration property,
     * and the sampled points will be evenly distributed.
     * @returns An object containing arrays of point indices and positions.
     */
    getSampledPoints(): {
        indices: number[];
        positions: number[];
    };
    /**
     * For the links that are currently visible on the screen, get a sample of link indices with their midpoint coordinates and angle.
     * The resulting number of links will depend on the `linkSamplingDistance` configuration property,
     * and the sampled links will be evenly distributed (one link per grid cell, based on link midpoint in screen space).
     * Each value is [x, y, angle]: position in data space; angle in radians for screen-space rotation (0 = right, positive = clockwise, e.g. for CSS rotation).
     */
    getSampledLinkPositionsMap(): Map<number, [number, number, number]>;
    /**
     * For the links that are currently visible on the screen, get a sample of link indices, midpoint positions, and angles.
     * The resulting number of links will depend on the `linkSamplingDistance` configuration property,
     * and the sampled links will be evenly distributed.
     * Positions are in data space; angles are in radians for screen-space rotation (0 = right, positive = clockwise, e.g. for CSS rotation).
     */
    getSampledLinks(): {
        indices: number[];
        positions: number[];
        angles: number[];
    };
    /**
     * Gets the X-axis of rescaling function.
     *
     * This scale is automatically created when position rescaling is enabled.
     */
    getScaleX(): ((x: number) => number) | undefined;
    /**
     * Gets the Y-axis of rescaling function.
     *
     * This scale is automatically created when position rescaling is enabled.
     */
    getScaleY(): ((y: number) => number) | undefined;
    /**
     * Start the simulation. Data ingest and the initial render belong to `render()`;
     * this method sets the simulation running and requests frames to drive it.
     * An active position transition is ended immediately (`onTransitionEnd`
     * fires with `interrupted: true`).
     * If the simulation is already running, calling `start(alpha)` reheats it by
     * resetting `alpha` and `simulationProgress` without firing
     * `onSimulationStart` again.
     * @param alpha Value from 0 to 1. The higher the value, the more initial energy the simulation will get.
     */
    start(alpha?: number): void;
    /**
     * Stop the simulation. This stops the simulation and resets its state.
     * Use start() to begin a new simulation cycle.
     */
    stop(): void;
    /**
     * Pause the simulation. When paused, the simulation stops running
     * but preserves its current state (progress, alpha).
     * Can be resumed using the unpause method.
     */
    pause(): void;
    /**
     * Unpause the simulation. This method resumes a paused
     * simulation and continues its execution.
     * An active position transition is ended immediately (`onTransitionEnd`
     * fires with `interrupted: true`).
     */
    unpause(): void;
    /**
     * Run one step of the simulation manually.
     * Works even when the simulation is paused.
     */
    step(): void;
    /**
     * Sets the view directly, bypassing the interactive zoom behavior — for hosts
     * (deck.gl layers, map renderers) that own the camera and want cosmos.gl's full
     * rendering (`drawToRenderPass`) to follow it. Updates everything the zoom
     * gesture would: the shader projection matrix, picking, point-radius zoom
     * scaling, and the space↔screen conversion methods.
     *
     * The transform follows the d3-zoom convention. With `S = spaceSize` (adjusted),
     * `[w, h] = screenSize` in CSS pixels, a point at space position
     * `(spaceX, spaceY)` lands on screen (y down) at:
     * ```
     * screenX = k * (spaceX + (w - S) / 2) + x
     * screenY = k * ((S - spaceY) + (h - S) / 2) + y
     * ```
     * (cosmos space y points up; screen y points down.)
     *
     * @param transform - View transform: `k` scale (screen pixels per space unit),
     *   `x`/`y` translation in screen pixels.
     * @param screenSize - Viewport size in CSS pixels. Required for headless
     *   instances (they have no canvas to measure); ignored when omitted on a
     *   non-headless instance, whose canvas remains the source of truth.
     */
    setViewTransform(transform: {
        k: number;
        x: number;
        y: number;
    }, screenSize?: [number, number]): void;
    /**
     * Records the point and link draws into a render pass owned by the host,
     * without clearing, ending, or submitting it. Lets an embedding application
     * (deck.gl layer, map renderer) compose cosmos.gl's rendering into its own
     * frame: begin a pass, call this, draw other content, end and submit.
     *
     * Draws with cosmos.gl's current view transform and screen size. A headless
     * instance has no view of its own — the host supplies one through
     * `setViewTransform()` before drawing (or samples `getPointPositionTexture()`
     * with its own shaders instead). No-op until data has been provided and
     * `render()` called.
     *
     * With `picking`, the pass draws picking colors instead of the graph: every fragment a
     * point or link covers carries its index + 1 as RGB bytes (point `i` as `i + 1`, link
     * `j` as `linkIndexOffset + j + 1`) and `picking.alpha`, with hard edges and no blending.
     * That is the encoding deck.gl decodes — a deck layer records this in deck's pick pass
     * with the pass's constant alpha — and most hosts can. Decode as
     * `r + g * 256 + b * 65536 - 1`; 0 is "nothing here". Dash gaps count as covered, as in
     * the engine's own hover picking.
     *
     * @param renderPass - The host's open render pass to record into.
     * @param options - Set `points` or `links` to `false` to draw only the other; `picking`
     *   draws picking colors with the given `alpha`, links offset by `linkIndexOffset`.
     */
    drawToRenderPass(renderPass: RenderPass, options?: {
        points?: boolean;
        links?: boolean;
        picking?: {
            alpha: number;
            linkIndexOffset?: number;
        };
    }): void;
    /**
     * Hover a point that a host found with its own picking, such as deck.gl's pick pass.
     * The point gets the hover ring (`hoveredPointRingColor`), whatever
     * `renderHoveredPointRing` says — that flag gates only cosmos.gl's own pointer hover.
     * Kept apart from the pointer hover: no mouse callbacks fire, and focus is not touched.
     * A config update keeps it.
     * @param index - The point to hover, or `undefined` to clear.
     */
    setHostHoveredPoint(index?: number): void;
    /**
     * Hover a link that a host found with its own picking. The link gets the hovered-link
     * width and color (`hoveredLinkWidthIncrease`, `hoveredLinkColor`); while set, it is the
     * hovered link cosmos.gl draws. Same rules as `setHostHoveredPoint`.
     * @param index - The link to hover, or `undefined` to clear.
     */
    setHostHoveredLink(index?: number): void;
    /**
     * Render a single frame immediately, without scheduling any further frames.
     * Intended for hosts that drive cosmos.gl from their own scheduler
     * (`enableRenderLoop: false`): call `renderOneFrame()` from the host's frame
     * callback. Performs the same per-frame work as the internal loop — screen-size
     * sync, transitions, hover detection, one simulation step (when the simulation
     * is running), and the draw — including the simulation-end check.
     * No-op on headless instances (they own no canvas to draw into).
     */
    renderOneFrame(): void;
    /**
     * Destroy this Cosmos instance.
     */
    destroy(): void;
    /**
     * Uploads the processed data to the GPU for every channel whose update flag is set
     * (positions, colors, sizes, shapes, images, links, forces, clusters) and requests a redraw.
     * An internal stage of the render pipeline: it does not ingest new input arrays, so data
     * passed to `setPointPositions`, `setPointColors`, `setLinks`, etc. takes effect only on
     * the next `render()`. To apply new data while keeping the current alpha, call
     * `render(undefined)` — or `render(undefined, 0)` to also snap instead of animating.
     */
    create(): void;
    /**
     * Converts an array of tuple positions to a single array containing all coordinates sequentially
     * @param pointPositions An array of tuple positions
     * @returns A flatten array of coordinates
     */
    flatten(pointPositions: [number, number][]): number[];
    /**
     * Converts a flat array of point positions to a tuple pairs representing coordinates
     * @param pointPositions A flattened array of coordinates
     * @returns An array of tuple positions
     */
    pair(pointPositions: number[]): [number, number][];
    /**
     * Restores init-only fields (`initialZoomLevel`, `randomSeed`, `attribution`)
     * to their pre-update values, preventing runtime changes via setConfig/setConfigPartial.
     */
    private preserveInitOnlyFields;
    private getFitViewPositions;
    /**
     * Compares the previous config snapshot with the current `this.config` and
     * applies any necessary side effects (updating renderers, store, behaviors, etc.).
     */
    private updateStateFromConfig;
    /**
     * Applies `enableSimulation` lifecycle changes triggered by config updates.
     */
    private applyEnableSimulationConfigChange;
    /**
     * Ensures device is initialized before executing a method.
     * If device is not ready, queues the method to run after initialization.
     * @param callback - Function to execute once device is ready
     * @returns true if device was not ready and operation was queued, false if device is ready
     */
    private ensureDevice;
    /**
     * Installs the pointer/keyboard/zoom/drag handlers on the canvas and document.
     * Never called for headless instances — they own no canvas and no input.
     */
    private initInteractions;
    /**
     * Validates that a device has the required HTMLCanvasElement canvas context.
     * Cosmos requires an HTMLCanvasElement canvas context and does not support
     * OffscreenCanvas or compute-only devices.
     * @param device - The device to validate
     * @returns The validated canvas context (guaranteed to be non-null and HTMLCanvasElement type)
     * @throws Error if the device does not meet Cosmos requirements
     */
    private validateDevice;
    /**
    * Updates and recreates the graph visualization based on pending changes.
    *
    * @param simulationAlpha - Optional alpha value to set. If not provided, keeps current alpha.
    */
    private update;
    /**
     * Runs one step of the simulation (forces, position updates, alpha decay).
     * This is the core simulation logic that can be called by step() or during rendering.
     *
     * @param forceExecution - Controls whether to run the simulation step when paused.
     *   - If true: Always runs the simulation step, even when isSimulationRunning is false.
     *     Used by step() to allow manual stepping while the simulation is paused.
     *   - If false: Only runs if isSimulationRunning is true. Used during rendering
     *     to respect pause/unpause state.
     */
    private runSimulationStep;
    private initPrograms;
    /**
     * Schedules a frame if none is pending. The RAF callback renders once and
     * reschedules only while `shouldKeepRendering()` — when the scene is static,
     * no frames run at all.
     */
    private frame;
    /**
     * Request a redraw. No-op if a frame is already scheduled. All internal
     * events that can change visual state funnel through this.
     */
    private requestRender;
    /**
     * Whether the loop must continue past the current frame — true while any
     * continuous activity can still change visual state.
     */
    private shouldKeepRendering;
    /**
     * Whether findHoveredItem() still has work to do. Keeps the loop alive
     * through the hover throttle (up to MAX_HOVER_DETECTION_DELAY frames) until
     * detection actually executes — which updates _lastCheckedMouseX/Y and
     * consumes _shouldForceHoverDetection, turning this false.
     */
    private hasPendingHoverWork;
    /**
     * Renders a single frame. This does NOT schedule the next frame.
     * On an external device the frame runs under cosmos's GL state and hands the
     * host its own back afterwards.
     */
    private renderFrame;
    /** The actual rendering logic of a frame. */
    private drawFrame;
    private stopFrames;
    /**
     * Called automatically when simulation completes (alpha < ALPHA_MIN).
     * Rendering continues after this is called (for rendering/interaction).
     */
    private end;
    private onClick;
    private updateMousePosition;
    private onContextMenu;
    /** Clear the pending touch/pen long-press timer, if any. */
    private cancelLongPress;
    /**
     * Dispatch the contextmenu callback chain — shared between the desktop
     * `contextmenu` handler and the touch/pen long-press timer.
     */
    private fireContextMenu;
    /**
     * Updates cosmos state to match the canvas's CSS size:
     * - `store.screenSize`
     * - the zoom transform — re-centered so the view keeps looking at the same spot
     * - the screen-sized sampling grids and picking FBOs
     *
     * Does NOT touch `canvas.width`/`canvas.height` — the drawing buffer belongs
     * to luma.gl's autoResize.
     *
     * Skips the work when the CSS size is unchanged. Pass `force` when state must
     * be rebuilt at the same size (e.g. `spaceSize` remaps the world).
     */
    private syncScreenSize;
    private updateZoomDragBehaviors;
    /**
     * Only steal touch gestures when cosmos uses them. With both flags off
     * the page can scroll over the canvas.
     */
    private updateCanvasTouchAction;
    private findHoveredItem;
    /** Marks both picking buffers stale when the view transform changed since the last check. */
    private updatePickingBufferStaleness;
    /**
     * Marks the point and link picking buffers stale — the scene they were
     * rasterized from (positions, sizes/widths, status, view transform) changed.
     */
    private markPickingBuffersStale;
    /** Applies the results of async picks once the GPU readbacks complete. */
    private resolvePendingPick;
    /**
     * Applies point and link pick results and fires the hover callbacks —
     * `undefined` for either means "no new information, keep the current state".
     * On the async path the pointer event that triggered the picks is already
     * gone, so `currentEvent` may be undefined — same as for detections forced
     * without a fresh event.
     */
    private processHoverResult;
    /** Applies a picked point to the store. Returns flags for deferred callback firing. */
    private applyPickedPoint;
    /** Applies a picked link to the store. Returns flags for deferred callback firing. */
    private applyPickedLink;
    private updateCanvasCursor;
    private addAttribution;
}
export type { GraphConfig, GraphSimulationConfig, GraphSimulationConfigInterface } from './config';
export { GraphSimulation } from './simulation';
export type { PointPositionTexture, PositionTextureSource } from './simulation';
export type { PositionsReadOptions } from './simulation';
export type { PointTracker } from './modules/Points/point-tracker';
export { PointShape, LinkStyle } from './modules/GraphData';
export type { LinksByPoint } from './modules/GraphData';
export { TransitionEasing } from './modules/Transition';
export * from './variables';
export * from './helper';
