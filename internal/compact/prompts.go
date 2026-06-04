package compact

// Summarization prompts ported from Pi's compaction module: structured
// markdown sections the next context build can rely on, plus explicit
// file-list tags. Serialized conversations are wrapped in <conversation>
// tags by the caller (buildSummaryRequest).

// summarizationPrompt produces the first summary of a session window.
const summarizationPrompt = `You are summarizing a coding-agent conversation so it can be continued with limited context.

Analyze the conversation above and produce a structured summary in markdown with exactly these sections:

## Goal
The user's overall objective(s), in one or two sentences.

## Constraints & Preferences
Requirements, style preferences, and explicit instructions the user gave. Quote exact values (names, versions, thresholds) rather than paraphrasing them.

## Progress
### Done
- [x] Completed work, with the files involved
### In Progress
- [ ] Work that was underway when the conversation was cut
### Blocked
- [ ] Anything that could not proceed, and why

## Key Decisions
Decisions made and their rationale, including approaches that were tried and rejected.

## Next Steps
What should happen next, in priority order. Be specific enough that work can resume without re-reading the original conversation.

## Critical Context
Anything else essential to continue: error messages, command invocations, API shapes, gotchas discovered.

After the sections, list every file that was read or modified in the conversation inside these tags:
<read-files>
one path per line
</read-files>
<modified-files>
one path per line
</modified-files>

Be concise but complete. Do not invent details that are not in the conversation. Do not continue the conversation; only summarize it.`

// updateSummarizationPrompt folds new conversation into an existing summary
// (subsequent compactions). The previous summary is provided by the caller
// inside <previous-summary> tags.
const updateSummarizationPrompt = `You are updating an existing summary of a coding-agent conversation so it can be continued with limited context.

The previous summary appears above inside <previous-summary> tags; the conversation that happened after it appears inside <conversation> tags.

Produce an updated summary with the same structure (## Goal, ## Constraints & Preferences, ## Progress with ### Done/In Progress/Blocked, ## Key Decisions, ## Next Steps, ## Critical Context, then <read-files> and <modified-files> tag blocks):
- PRESERVE everything from the previous summary that is still true.
- ADD new progress, decisions, and context from the new conversation.
- UPDATE items whose status changed (e.g. move In Progress items to Done).
- Merge the file lists: keep previously listed files and add newly touched ones.

Be concise but complete. Do not invent details. Do not continue the conversation; only summarize it.`

// turnPrefixPrompt summarizes the beginning of a turn that was split by the
// cut point: tool calls and results already consumed, before the kept tail.
const turnPrefixPrompt = `You are summarizing the first part of an in-progress coding-agent turn; the rest of the turn remains in context verbatim.

Summarize compactly what the assistant was doing in the conversation above: the task it was working on, the tool calls it made and what they returned, and any conclusions reached so far. Keep it short — a few sentences or bullets. Do not speculate about what happens next. Do not continue the conversation; only summarize it.`

// branchSummaryPrompt condenses an abandoned conversation branch when the
// user navigates back to an earlier point.
const branchSummaryPrompt = `You are summarizing a conversation branch that the user explored and then abandoned by returning to an earlier point.

Summarize compactly what was attempted on the branch above: the goal, what was tried, what was learned (including failures and dead ends), and any files that were touched. A few sentences or bullets. This summary is a breadcrumb, not a continuation — do not propose next steps.

List touched files inside <read-files> and <modified-files> tags as above, when any.`

// branchSummaryPreamble introduces a branch summary when it enters context.
const branchSummaryPreamble = "The user explored a different conversation branch before returning here.\nSummary of that exploration:"

// pinnedExclusionNote is appended to summarization prompts when pinned
// entries exist: they survive compaction verbatim, so re-describing them
// wastes summary budget.
const pinnedExclusionNote = "Do not re-describe content of pinned messages; they remain in context verbatim."
