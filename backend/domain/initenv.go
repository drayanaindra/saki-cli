package domain

// InitEnvStatus is the closed status vocabulary for the mutating init-env result.
type InitEnvStatus string

const (
	InitEnvStatusOK          InitEnvStatus = "ok"
	InitEnvStatusFailed      InitEnvStatus = "failed"
	InitEnvStatusNotVerified InitEnvStatus = "not_verified"
)
