/**
 * Decoded-data renderers (see views.tsx). None of these imports the decoder
 * eagerly: BytesValue loads it on demand to decode nested calldata.
 *
 *   <DecodedCallView call={DecodedCall} />      a decoded function call
 *   <DecodedEventView event={DecodedEvent} />   a decoded log
 *   <ParamsTable params={[{ name, type, value, components? }]} />
 *       any named, typed values: our API's DecodedParam[] or viem decode
 *       output (bigint, arrays, objects keyed by component name)
 *   <ParamValueView type value nested? hashed? />  one leaf value
 *   <NodeView node={DecodedNode} />             one node of a decoded tree
 *   toNode(name, type, value, components?)      (type, value) -> DecodedNode
 */
export {
  AddressValue,
  BytesValue,
  DecodedCallView,
  DecodedEventView,
  NodeView,
  ParamValueView,
  ParamsTable,
  UintValue,
  toNode,
  type ParamComponent,
  type ParamInput,
} from "./views";
