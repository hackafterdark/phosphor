# RFC: Chat View Performance Optimization for Long Sessions

## Status

Draft

## Problem

During long sessions with many messages (200+), the Phosphor TUI becomes noticeably slow. Scrolling, typing new prompts, and stopping processes feel laggy. The bottleneck is the UI rendering pipeline, not the AI API calls.

### Symptoms

- Typing in the editor feels delayed
- Scrolling through the message list is sluggish
- The TUI becomes unresponsive during streaming (frame drops)
- Memory usage grows proportionally with session length

### Root Cause Analysis

The rendering pipeline has three layers of caching, but gaps between them create per-frame work that scales poorly:

1. **List-level cache** (`list/list.go`): Caches rendered output per item keyed by `(width, version)`. Frozen entries (where `Finished()` returns true) skip re-rendering. This works well for stable items but invalidates on width changes.

2. **Item-level cache** (`chat/messages.go`): `cachedMessageItem` stores rendered output by width. Each message type caches its own output, invalidating when content changes.

3. **Draw cache** (`model/chat.go`): The `chatDrawCache` pre-decodes the full `list.Render()` output into a `uv.ScreenBuffer`, avoiding per-frame ANSI reparse. However, it's invalidated on every content change — which happens on every streaming tick.

The gap: **the draw cache is per-frame, not per-item**. When any message changes, the entire rendered string is re-decoded. During streaming, this means full ANSI decode + buffer creation every frame. Additionally, there is no cap on the number of messages in the list, so a session with 200+ items means 200+ cached entries, each consuming memory and layout computation time on scroll/resize.

## Proposed Solution

Implement a layered set of optimizations, each addressing a different part of the rendering pipeline.

### Phase 1: Message Cap (Highest ROI)

Limit the number of messages kept in the UI list. Old messages remain in session data but are removed from the render pipeline.

```go
// In internal/ui/model/chat.go

const maxMessages = 300

func (m *Chat) SetMessages(msgs ...chat.MessageItem) {
    if len(msgs) > maxMessages {
        msgs = msgs[len(msgs)-maxMessages:]
    }
    m.list.SetItems(msgs...)
}
```

**Why 300?** A typical session with 3-4 tool calls per assistant response yields ~15-20 messages per round. 300 messages covers ~15-20 rounds, which is well beyond what a user would need to scroll back through in a single session.

**Trade-off:** Users can't scroll to the very top of very long sessions. However, they can still view old messages by opening the session in the session list (which loads the full history). For most use cases, the last 15-20 rounds of conversation is the relevant context.

**Implementation notes:**
- The cap is applied at the UI layer only, not the session data layer
- When loading a session with more than `maxMessages` items, only the tail is shown
- A "load more" button could be added later if needed (see Future Potential)

### Phase 2: Assistant Message Truncation

Apply the same truncation pattern used for tool outputs (`responseContextHeight = 10` in `chat/tools.go`) to assistant messages with large content blocks (thinking, code, tool results).

```go
// In internal/ui/chat/assistant.go

const assistantMessageMaxLines = 50

// When rendering an assistant message, truncate content beyond
// assistantMessageMaxLines, showing a "N lines hidden" indicator
// that can be expanded by the user.
```

This prevents a single assistant message with a large tool output or thinking block from dominating the viewport and cache.

### Phase 3: Layered Draw Cache

Replace the single monolithic `chatDrawCache` with per-item screen buffers. On each frame:

1. **Stable items** (not streaming, no content change): Use their cached `uv.ScreenBuffer` directly.
2. **Changed items** (streaming, new content): Re-render only those items.
3. **Composed output**: Blit stable buffers + newly rendered items into the final screen area.

This eliminates the full-string re-decode on every streaming tick. Only the streaming item's buffer is recreated; all others are reused.

**Implementation sketch:**
```go
type itemDrawCache struct {
    renderHash string  // simple hash of rendered content
    buf        uv.ScreenBuffer
}

type layeredDrawCache struct {
    items map[chat.MessageItem]*itemDrawCache
}

func (c *layeredDrawCache) Draw(scr uv.Screen, area uv.Rectangle, items []chat.MessageItem) {
    for i, item := range items {
        entry := c.getOrRebuild(item, i)
        entry.buf.Draw(scr, itemArea)
    }
}
```

**Complexity:** Higher than Phase 1. Requires tracking per-item render hashes and managing buffer lifetimes. Worth doing once Phase 1 is proven effective.

### Phase 4: Streaming Debounce

During streaming, batch updates so rendering happens at most once every 50-100ms instead of on every tick. This reduces the number of draw cache invalidations during active streaming.

```go
// In the agent/stream handler
const streamRenderInterval = 80 * time.Millisecond

// Use a timer to throttle render messages
streamTimer := time.NewTimer(streamRenderInterval)
defer streamTimer.Stop()

for chunk := range streamCh {
    appendChunk(chunk)
    select {
    case <-streamTimer.C:
        m.refreshChat()
        streamTimer.Reset(streamRenderInterval)
    default:
        // Skip this frame
    }
}
```

**Trade-off:** Streaming content appears slightly less smooth (updated at ~12fps instead of 60fps), but the difference is imperceptible for text streaming. The performance gain is significant: fewer draw cache invalidations per second.

### Phase 5: Smaller Screen Buffer

Currently, `View()` creates a `uv.NewScreenBuffer(m.width, m.height)` for the full terminal dimensions. The chat viewport is only a fraction of this. Use a buffer sized to the chat area only, and compose it with header/status/editor regions separately.

```go
// Instead of:
canvas := uv.NewScreenBuffer(m.width, m.height)

// Use:
chatHeight := m.layout.main.Dim().Height
canvas := uv.NewScreenBuffer(m.layout.main.Dim().Width, chatHeight)
```

**Impact:** Reduces the number of cells processed by `canvas.Render()` from ~20,000 (full terminal) to ~3,000 (chat area only). The header, status bar, and editor are small enough that their individual renders are negligible.

## Existing Optimizations (Already in Place)

Before implementing, note what's already working well:

- **Viewport-bounded list rendering**: `list.Render()` only renders items within the viewport, bounded by `l.height` lines. Never processes beyond the visible budget.
- **Frozen entries**: Once `Finished()` returns true, items are cached and never re-rendered.
- **Tool output truncation**: `responseContextHeight = 10` limits tool output display.
- **Item-level caching**: `cachedMessageItem` stores rendered output by width.

The proposed changes build on these foundations rather than replacing them.

## Charm Package Support

- **Ultraviolet**: Provides optimized screen buffer rendering with cell-copy operations. Used by the draw cache. No built-in "render only visible" abstraction.
- **Bubble Tea v2**: Virtual terminal model with input buffering. No built-in virtual scrolling.
- **Catwalk**: Golden file testing only, not relevant to runtime performance.

None of the charm packages provide a virtual scrolling or view-port-only rendering abstraction. That's the list package's responsibility, which Phosphor already implements well.

## Testing

1. **Load test:** Run a session with 500+ tool calls. Verify the list never exceeds 300 items and rendering remains responsive.
2. **Scroll test:** Open a long session and scroll through it. Verify no frame drops or lag.
3. **Streaming test:** Start a streaming session. Verify the debounced render interval doesn't make text appear choppy.
4. **Memory test:** Profile memory before and after the message cap. Expect ~40-60% reduction in UI-related memory for long sessions.
5. **Regression test:** Verify that the session list still shows full history for all sessions (the cap is UI-only).

## Future Potential

### "Load More" Button

Instead of silently dropping old messages, show a "Load older messages" button at the top of the chat. Clicking it expands the visible range. This preserves full scrollability while keeping the default rendering budget bounded.

### Configurable Cap

Make `maxMessages` configurable via `phosphor.json`:

```json
{
  "ui": {
    "maxChatMessages": 300
  }
}
```

Different workflows may need different caps. Power users with long debugging sessions might want 500; users on low-memory devices might want 150.

### Per-Item Layered Cache

Phase 3's layered draw cache could be extended to support partial invalidation: only the streaming item's buffer is recreated, while all others are reused. This would eliminate the full-string re-decode entirely during streaming.

### Virtual Scroll for Session List

The session list (in `dialog/sessions.go`) could also benefit from virtual scrolling if it ever grows beyond the viewport. Currently it renders all sessions, but the number of sessions is typically small (< 50).
