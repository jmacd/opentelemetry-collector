# Internal queue and batch telemetry

This package connects queue and batch operations to component-specific metrics.
It is internal because the callbacks are concrete implementation details.
External users should not depend on this arrangement.

The component creates an `ObsMetrics` value backed by its generated metric
instruments. It then calls `ConfigWithObsMetrics` to attach the callbacks to its
`component.Config`. This avoids adding an internal option to the public
exporterhelper API.

Each exporterhelper constructor removes the wrapper immediately. The original
config continues through normal validation. The callbacks become an internal
exporterhelper option.

Exporterhelper owns the callbacks after its options are applied successfully.
It calls `Shutdown` when construction later fails or when the component shuts
down. Until ownership transfers, the component must call `Shutdown` if
construction fails.

If an external implementation is needed, replace this arrangement with a
public interface designed for that use.
