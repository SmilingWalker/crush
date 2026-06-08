Use this tool when you need to ask the user questions during execution. This allows you to:
1. Gather user preferences or requirements
2. Clarify ambiguous instructions
3. Get decisions on implementation choices as you work
4. Offer choices to the user about what direction to take

Usage notes:
- Users will always be able to select "Other" to provide custom text input
- Use multi_select: true to allow multiple answers to be selected for a question
- In multi-select, users can combine regular options with "Other" for a custom answer alongside their selections
- If you recommend a specific option, make that the first option and add "(Recommended)" at the end of the label

Preview:
- Add a "preview" field to options when presenting concrete artifacts that users need to visually compare:
  ASCII mockups of UI layouts, code snippets showing different implementations, diagram variations, or configuration examples
- Preview content is rendered as markdown below the options list. Keep it concise (up to 5 lines, truncated if longer)
- Do not use previews for simple preference questions where labels and descriptions suffice
- Keep option descriptions under 40 characters — use preview for longer explanations

Constraints:
- Ask 1-4 questions at a time
- Each question must have 2-4 options
- Question texts must be unique
- Option labels must be unique within each question
- Header must be 1-12 characters

Plan mode:
- Use this tool to clarify requirements or choose between approaches BEFORE finalizing your plan
- Do NOT ask "Is my plan ready?", "Should I proceed?", or reference "the plan" in your questions — the user cannot see the plan details in the UI

Annotations:
- Users can press "n" to add notes to any question
- Notes are returned with the answer and can provide additional context
- Example: user selects "OAuth" and adds note "use RS256 signing"
