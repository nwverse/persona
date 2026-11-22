# 收件箱模块接入

`inboxEntries(navigator, repository)` 注册列表和 `LarkRoute`。也可用 `LocalInboxRepository` 注入；未传入时通过 Hilt 获取仓库。本轮不修改 `android/app/`。

## 当前 App 接线与剩余包装

App 的 `InboxModule` 已按配对注入 `RealInboxRepository`、共享偏好／快照、`HttpInboxActionsTransport`、主对话发送器和路由更新的 `HttpMailTransport`，列表筛选也已接配对 DataStore。`InboxExperienceRepository` 尚未包装到实际入口，回复编辑器仍由页面持有；接入时每个 `HubScope` 只创建一个实例，参数如下：

- `storage`：实现 `InboxStateStorage`，接共享加密存储的独立记录。按 `hub_id + 配对代次` 隔离，`write` 必须原子提交，作用域失效后拒绝旧实例读写。不要使用全局偏好文件。退出、切 hub 或重新配对时随该作用域删除。
- `ownerScope`：当前配对的协程作用域，切页保持运行；撤销配对时取消。它持有回复编辑器，避免离页取消发送或丢掉草稿写入。
- `connectionState`：运行中 `HubConnection.state`。`invalidations` 接收 inbox 事件；恢复在线时自动拉取列表并核对待确认回复。
- `actions`：新 `HttpInboxActionsTransport`，使用共享 HTTP client、当前配对 token、作用域有效性判断；`endpoint` 每次返回活动连接的 HTTP 地址。下拉和刷新按钮调用 `POST /api/v1/inbox/refresh`，随后读取列表。
- `sendToPersona`：接统一主对话用例，根据 `Summarize` / `DraftReply` 生成总结或拟回复请求，携带事项 ID 和完整上下文。请求可靠入盘后返回，并由 App 导航到 Persona tab。不能直接发给来源会话。

包装层在联网前读取缓存，持久保存筛选、折叠、搜索和回复草稿。成功读取空列表才清空缓存，刷新失败保留已有内容。不同筛选组合分别保存页面内的滚动位置。列表使用主题语义色，来源标位于头像角上，账号名允许完整换行，按钮文字单行。深浅色真机观感未验收，性能未实测。

## 回复与核对

发送前保存草稿及原 `client_id`，再调用原仓库发送。回执不明或进程中断时保留“结果待确认”，再次操作只核对，不重新发送。飞书核对刷新窗口后匹配 `client_id`，不按文本相同判断；成功后只清除 ID 相符的草稿。草稿读取失败时禁用发送，保存失败时保留输入。

当前服务端 `/inbox/{id}/reply` 已读取 `client_id` 并通过 `withReplyReceipt` 持久重放原结果；客户端按原 ID 核对，无须另增邮件专用回执查询接口。回复 journal 与普通 outbox 尚未统一，真实来源回显和 hub 重启后的防重仍需验收。飞书发送接口已接收 `client_id`，但真实来源是否回显该字段仍需集成验收。

“已处理”沿用原仓库的乐观更新和失败回滚；飞书详情仅成功后返回列表。飞书页面已消费 `core:chatlist`；飞书可见已读和窗口补洞见下文「飞书会话」，邮件详情见「邮件详情接线」。

## 飞书会话

飞书会话保留现有倒序 `LazyColumn`，已补齐左侧头像、引用条、编辑标记、机器人底色、系统消息和日期分隔。事项入口先按位置定位，再按行高修正；用户开始拖动后才自动上下补页。已读取消息正文与视口的实际交集，页面恢复前台、窗口有焦点、定位完成、没有遮挡、滚动停止且窗口经过网络核对时才提交。页面关闭或门禁变化会取消尚未发送的已读任务。

`LarkInboxRepository` 包装现有仓库，继续复用草稿、发送和 outbox。它负责围绕入口读取窗口、单页双向翻页、回到最新、原位核对编辑和撤回，以及只向前提交可见消息的已读位置。打开的会话监听连接状态和 `LarkInvalidation`，连接恢复 Online 或收到对应会话的事件时核对当前窗口。按服务器游标连续读取，不按位置数字是否相邻判断缺口。`FileLarkWindowCache` 先画覆盖事项起点的缓存，网络核对前禁用已读；缓存按作用域、会话和入口位置隔离。

飞书剩余接线：

- 用现有配对作用域的 `InboxAssistantTransport`、hub URL、凭据、HTTP client 和作用域有效性检查构造 `HttpLarkConversationTransport`，随配对变化更新 `StateFlow`。作用域必须包含 hub 和账号身份。
- 用 `LarkInboxRepository` 包装现有仓库，提供连接状态流、原始 `lark` 事件经 `LarkInvalidation.decode` 得到的事件流，以及放在应用私有 `noBackupFilesDir` 下的 `FileLarkWindowCache`，再注入 `inboxEntries`。当前共享连接已解码 lark／inbox 事件，RealInboxRepository 也已订阅；包装器复用同一事件源。
- App 内浮层遮住会话时，设置 `LocalLarkReadOccluded`；系统弹窗还受窗口焦点门禁约束。
- 两层包装同时使用时，`InboxExperienceRepository` 放在最外层、包住 `LarkInboxRepository`：页面只从最外层取它持有的回复编辑器（`rememberReplyComposers`）。
- LarkScreen 已使用共用列表的阅读锚点、冻结和回最新；接包装器时保留 markVisible、loadOlder、loadNewer、latestConversation 和稳定消息 key。目前 App 代理到 RealInboxRepository 的 markVisible／loadNewer 仍为默认空实现。
- DATA-06 后续提供独立的 outbox 消息流，替换包装器当前对旧 `conversation` 流的订阅。现阶段该订阅保留旧仓库的轮询，用来恢复待发送、失败和已确认的本地气泡；新包装器未注入前，旧仓库继续原有行为，不会上报已读。

本轮只做模块编译和关键单元测试，深浅色使用 Material 语义色，尚未做模拟器截图验证。性能未实测。

## 邮件详情接线

`InboxScreen` 的邮件行和 `inboxEntries` 已接 MailRoute。App 已注入随配对／受信路由更新的 HttpMailTransport；邮件 AI 经仓储 sendToMain，网页链接走默认 LarkLinkHost。以下是宿主接入方式，当前默认入口已具备：

- 创建与当前配对绑定的 `HttpMailTransport(scope, baseUrl, token, client, isCurrent)`。`client` 使用现有 `HubHttpClient.client`，切换或断开配对时替换实例，并让旧实例的 `isCurrent` 返回 false。通过 `inboxEntries(mailTransport = …)` 或 `LocalMailTransport` 注入。
- 传入 `onMailAskAi`，把 `MailAiContext.text` 作为完整邮件上下文带到 Persona 助手。上下文包含收发件人、正文和附件清单，不含附件内容。回调只准备助手上下文，不应直接发送邮件；当前默认 AI 操作已通过共享 sendToMain 发送。
- 页面底部是「回复」「交给 AI」和「AI 草拟」（`InboxAiActions`），和飞书会话页一致；原来的「总结 / 拟回复」（走 `askPersona`）已换成交给 AI 浮层和原地草拟，见 `docs/android/android.md`「Inbox AI drafting and handoff」。
- 传入 `onMailOpenLink`，通过现有应用内网页容器打开 HTTP／HTTPS 链接。邮件 WebView 不加载这些链接，不携带配对 token。

详情按事项的 `mail.uids` 显示最新一封，可切换到同事项其他邮件。HTML 经标签白名单过滤，移除原始样式与图片，再用主题前景色、背景色和链接色渲染。WebView 禁用脚本、网络资源和文件访问，按内容高度展开并在退出时销毁。邮件链接只由用户点击后交给 App。未做深浅色截图，实际布局和 WebView 高度仍待真机验收。

已读采用显式按钮，不在打开时自动上报。修改后重读邮件，再调用 `/inbox/refresh` 采集来源状态并刷新仓储；返回时再次读取事项列表。同步失败显示错误，可重试或明确选择先返回。回复使用现有持久化草稿与 outbox，成功后刷新服务端事项；只允许从最新一封回复。邮件正文和收件信息支持选择复制。所有按钮文字单行。

## hub 缺口

- `/inbox/{id}/reply` 已支持 client_id 持久回执及原 ID 重放，丢回执和重启防重仍需真实账号验收；旧 hub 的回执能力不能仅凭新客户端推定。
- 事项回复由服务端选择当时的最新邮件。建议增加可选的目标 UID／版本校验，防止用户编辑期间新邮件到达后回复对象变化。
- 附件只有文件名、类型和大小，没有下载接口或稳定附件标识。当前只展示清单；下载能力需要 hub 新增接口。
- 当前总结、拟回复和邮件上下文通过共享 sendToMain 进入 Persona，无需新增 AI 接口；独立 AI 分身委派不在本次入口接线范围。
- 已读接口与事项采集分离，当前通过 `/inbox/refresh` 再采集对齐。同步失败时不能承诺列表已更新。

## 邮件详情验证

已通过 `:feature:inbox:compileDebugKotlin`，以及 `HttpMailTransportTest` 和 `MailHtmlTest` 两个定向测试。覆盖邮件读取、空元数据、路径编码、已读更新、配对失效拦截，以及 HTML 危险内容过滤和主题颜色。未连接真实 hub，未做真机或截图验证。构建中 Kotlin daemon 写入目录受沙箱限制，自动回退后成功。新增中文说明与页面文案的半角标点、破折号扫描命中为零。

## 飞书卡片与原生文档

`LarkCardContent.kt` 解析飞书网关的 `metadata.header/body` 节点树，`LarkCardView.kt` 显示标题、字段列、只读按钮和鉴权图片。连续短字段共用一行，长字段独占一行；列按父容器宽度和权重分配，窄屏改为纵排。未知节点保留文字，整张卡片无法解析出正文时显示摘要。按钮不发送回调，也不打开操作链接，文案保持单行。

`LarkDocument.kt` 将文档 JSON 树转成标题、段落、列表、代码和图片。`LarkDocumentReader.kt` 负责阅读、网页降级和浮层。文档图片地址改写到 hub 的只读代理，经原配对鉴权读取；网页容器不接收配对凭据。尊重文档的复制权限，部分内容和未支持的块有提示，用户也可主动切换网页。

飞书会话只在 `LarkScreen` 外挂 `LarkLinkHost`，文字链接进入应用内浮层，打开时铺满，拖到六成高度可继续操作背后的会话，再拖关闭。浮层存在时暂停可见已读上报。共享聊天入口只在 `ChatLinkHost` 增加原生文档分支；Agent 和助手沿用原有浮层及网页降级。未修改 `ChatScreen`、`ChatChrome`、`AgentsScreen` 或 `android/app/`。

为使现有 App 注入直接可用，范围外的最小改动是 `InboxAssistantTransport` 新增默认只读方法、`HttpInboxAssistantTransport` 实现文档与同源图片请求，以及 `RealInboxRepository` 提供当前配对的 `contentSource` 并映射原生卡片。`InboxSnapshot.contentSource` 只在内存中使用，不写入缓存。新增聊天模块到收件箱模块的依赖，用于复用阅读器；没有修改服务端协议或 iOS。

验证包括收件箱模块单元测试、文档代理和媒体鉴权的定向测试，以及 `:app:assembleDebug`。已用临时离线预览入口检查一轮深浅色截图，临时入口已删除，截图位于 `/tmp/p2-lark-shots/`。截图覆盖卡片字段、按钮、图片与文档基础块，未连接真实 hub；真实文档权限、网页登录和复杂卡片仍需联调。性能未实测。

## 本次 App 接线

`InboxModule` 已在每个配对作用域创建 `HttpMailTransport`，复用活动路由和钉扎客户端。路由变化时替换传输，旧实例拒绝请求和响应；断网保留已加载的邮件内容。`inboxEntries` 从仓库取得邮件传输，邮件链接复用应用内阅读浮层，不携带配对凭据。

收件箱交接、总结、拟回复和邮件交接均调用 `MainConversationSender.sendToMain`，复用 `ScopedHubData.messages`。选中内容经 XML 转义后作为上下文，邮件包含收发件人、正文和附件清单。飞书事项详情在页面打开时预取服务端上下文并保存到配对缓存；未取到时使用已加载窗口或事项预览，提示上下文可能不完整。点击发送不等待网络，不先查询或创建主对话。操作 ID 保留供失败重试，入队成功后切换 Persona tab；待定状态使用 `PendingBadge` 和 `PendingStatus`。

本次沿用 `RealInboxRepository` 的配对作用域 journal 与列表偏好，没有启用上文的 `InboxExperienceRepository`；飞书列表引擎和外壳现已接通，可见已读／向后补页包装仍缺。邮件回复、已读和附件下载的 hub 限制见上文。本次不连接真实 hub，不做截图或性能验收；性能未实测。
