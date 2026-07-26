import { Schema } from 'effect'

export const protocolVersion = 1 as const
export const wireClientName = 'kiwi-code-web'

export const ChannelIDSchema = Schema.Number.pipe(
  Schema.int(),
  Schema.between(1, 0xffff_ffff),
)
export const SequenceSchema = Schema.Number.pipe(
  Schema.int(),
  Schema.greaterThanOrEqualTo(1),
)

export const OpenMessageSchema = Schema.Struct({
  t: Schema.Literal('open'),
  protocol: Schema.Literal(protocolVersion),
  client: Schema.String,
})
export const SubMessageSchema = Schema.Struct({
  t: Schema.Literal('sub'),
  id: ChannelIDSchema,
  topic: Schema.Unknown,
})
export const UnsubMessageSchema = Schema.Struct({
  t: Schema.Literal('unsub'),
  id: ChannelIDSchema,
})
export const ResnapMessageSchema = Schema.Struct({
  t: Schema.Literal('resnap'),
  id: ChannelIDSchema,
})
export const PingMessageSchema = Schema.Struct({
  t: Schema.Literal('ping'),
  ts: Schema.Number,
})
export const ClientMessageSchema = Schema.Union(
  OpenMessageSchema,
  SubMessageSchema,
  UnsubMessageSchema,
  ResnapMessageSchema,
  PingMessageSchema,
)
export type ClientMessage = Schema.Schema.Type<typeof ClientMessageSchema>

export const ReadyMessageSchema = Schema.Struct({
  t: Schema.Literal('ready'),
  protocol: Schema.Number,
  instanceId: Schema.String,
  serverTime: Schema.String,
})
export const SnapMessageSchema = Schema.Struct({
  t: Schema.Literal('snap'),
  id: ChannelIDSchema,
  seq: SequenceSchema,
  data: Schema.Unknown,
})
export const EventMessageSchema = Schema.Struct({
  t: Schema.Literal('event'),
  id: ChannelIDSchema,
  seq: SequenceSchema,
  data: Schema.Unknown,
})
export const SuberrorMessageSchema = Schema.Struct({
  t: Schema.Literal('suberr'),
  id: ChannelIDSchema,
  error: Schema.String,
})
export const SubendMessageSchema = Schema.Struct({
  t: Schema.Literal('subend'),
  id: ChannelIDSchema,
  reason: Schema.String,
})
export const PongMessageSchema = Schema.Struct({
  t: Schema.Literal('pong'),
  ts: Schema.Number,
})
export const ServerMessageSchema = Schema.Union(
  ReadyMessageSchema,
  SnapMessageSchema,
  EventMessageSchema,
  SuberrorMessageSchema,
  SubendMessageSchema,
  PongMessageSchema,
)
export type ServerMessage = Schema.Schema.Type<typeof ServerMessageSchema>
