package logging

//go:generate go tool options-gen -from-struct=Options -out-filename=options_generated.go
type Options struct {
	level string `option:"default:info" mapstructure:"level"`
	json  bool   `option:"default:false" mapstructure:"json"`
}
