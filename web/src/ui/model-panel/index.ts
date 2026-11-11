// The unified model panel (#5032). The pill imports ProviderMark from ./ProviderMark directly when it loads the panel
// itself lazily; this index is for places that take both.
export { ModelPanel, type ModelPanelProps } from "./ModelPanel";
export { ProviderMark } from "./ProviderMark";
