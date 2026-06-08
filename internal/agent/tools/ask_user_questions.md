Use this tool when you need to ask the user questions during execution.

Usage notes:
- Users will always be able to select "Other" to provide custom text input
- Use multi_select: true to allow multiple answers
- In multi-select, users can combine regular options with "Other" for a custom answer alongside their selections
- If you recommend a specific option, make it the first option and append "(Recommended)" to the label
- Add a "preview" field to options with markdown content to show a preview panel below the options
- Preview is useful for code snippets, file contents, or detailed descriptions
- Keep preview content concise (shown as up to 5 lines, truncated with ✂ if longer)
- Keep option descriptions under 40 characters — use preview for longer explanations

Constraints:
- Ask 1-4 questions at a time
- Each question must have 2-4 options
- Question texts must be unique
- Option labels must be unique within each question
- Header must be 1-12 characters

When to use:
- Gather user preferences or requirements
- Clarify ambiguous instructions
- Get decisions on implementation choices
- Offer choices about direction to take

Plan mode:
- Use this tool to clarify requirements BEFORE finalizing your plan
- Do NOT ask "Is my plan ready?" or "Should I proceed?"

Preview examples:
- Show code snippets for different implementation approaches
- Display file content previews for configuration options
- Present API response examples for endpoint selection

Annotations:
- Users can press "n" to add notes to any question
- Notes are returned with the answer and can provide additional context
- Example: user selects "OAuth" and adds note "use RS256 signing"
