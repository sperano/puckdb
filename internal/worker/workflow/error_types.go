package workflow

// errTypeInvalidInput is the Temporal application error type of a workflow
// input that can never succeed (unknown league, source or option). Such
// errors are non-retryable; clients match the type, not the message.
const errTypeInvalidInput = "InvalidInput"
