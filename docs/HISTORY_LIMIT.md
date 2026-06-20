# Chat History Pagination and Performance Configuration

Phosphor supports paginated loading of chat message history to ensure optimal performance, low latency, and low memory usage—even during long sessions with hundreds or thousands of messages.

---

## Rationale & Performance

In terminal user interfaces (TUIs), rendering large lists of components on every frame/update cycle can become CPU-intensive. When a session contains hundreds of messages, constructing and rendering each text area, tool call result, and syntax-highlighted block on every user action leads to:
* **Rendering Lag:** Noticeable delay when typing or scrolling.
* **Higher Memory Footprint:** Storing thousands of rich UI components in the TUI model state.
* **Startup Delay:** A long wait when entering or switching to a session as Phosphor processes the entire history.

To solve this, Phosphor loads session messages **lazily** using a sliding-window message pagination strategy.

---

## How It Works

1. **Initial Load:** When you switch to a session, Phosphor retrieves all message metadata lightweight but only instantiates the most recent `history_limit` messages as active UI components.
2. **The "Load More" Banner:** If there are more messages in the database than are currently loaded, a styled focusable banner is prepended to the top of the chat:
   ```
   ── ⏶ Load previous messages (X remaining) ──
   ```
3. **Loading Previous Messages:** Users can select the banner using the arrow keys and press `Enter`, or click it with the mouse. The banner shifts to a `⟳ Loading...` state, queries the database, and prepends the next `history_batch_size` messages.
4. **Scroll & Selection Stability:** When new messages are prepended, Phosphor automatically calculates the offset and adjusts the viewport/selection index. The current scroll position is preserved seamlessly, preventing the screen from jumping.
5. **Real-time Synchronization:** As new assistant responses or tool outputs arrive, they are dynamically appended and synchronize with the pagination window in real-time.

---

## Configuration Settings

You can customize the initial page size and subsequent batch size in your `phosphor.json` configuration file under the `options.tui` block:

```json
{
  "options": {
    "tui": {
      "history_limit": 100,
      "history_batch_size": 50
    }
  }
}
```

### Configuration Schema

| Setting | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `history_limit` | `integer` | `100` | The initial number of messages to load into the active chat list when opening or switching to a session. |
| `history_batch_size` | `integer` | `50` | The number of older messages to load and prepend to the conversation history when pagination is triggered. |
