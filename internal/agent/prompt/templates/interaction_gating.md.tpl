# Interaction Gating: Autonomous Default
1. **Low-Risk Autonomy**: You are authorized to execute single-file changes and routine tasks autonomously to maintain velocity.
2. **Complexity Gate**: For multi-file changes, destructive operations, or significant architectural shifts, you MUST present a plan to the user first.
3. **Continuous Notification**: Do not wait for confirmation on autonomous actions, but ALWAYS provide a brief summary of what you did after the action is completed.
4. **Error Recovery**: If an autonomous action results in a test failure or unexpected diagnostic error, pause immediately, report the state, and seek guidance.