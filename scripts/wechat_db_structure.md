# 微信数据库表结构分析

> 分析时间: 2026-02-23  
> 数据库路径: `~/Documents/xwechat_files/wxid_zdtd5hn0taqp31_d88a/db_storage/`  
> 加密方式: SQLCipher 4 (cipher_compatibility = 4)

---

## 1. 总体概况

已解开 **`message/message_0.db`**、**`bizchat/bizchat.db`**、**`message/biz_message_0.db`**、**`contact/contact.db`** 和 **`session/session.db`**（各使用独立密钥），其余数据库尚未解锁。

| 数据库 | 状态 | 说明 |
|--------|------|------|
| `message/message_0.db` | ✅ 可解密 | 聊天消息主库 |
| `contact/contact.db` | ✅ 可解密 | 联系人（7632 个） |
| `session/session.db` | ✅ 可解密 | 会话列表（268 个会话） |
| `favorite/favorite.db` | ❌ | 收藏 |
| `general/general.db` | ❌ | 通用配置 |
| `emoticon/emoticon.db` | ❌ | 表情包 |
| `sns/sns.db` | ❌ | 朋友圈 |
| `hardlink/hardlink.db` | ❌ | 硬链接资源 |
| `head_image/head_image.db` | ❌ | 头像 |
| `bizchat/bizchat.db` | ✅ 可解密 | 企业号聊天（表均为空） |
| `message/message_fts.db` | ❌ | 消息全文搜索索引 |
| `message/message_resource.db` | ❌ | 消息资源 |
| `message/media_0.db` | ❌ | 媒体文件 |
| `message/biz_message_0.db` | ✅ 可解密 | 公众号消息（1943 条） |

---

## 2. message_0.db 内部结构

共 **58 张表**：49 个 `Msg_<hash>` 消息子表 + 9 个辅助表。

### 2.1 Msg_\<hash\> — 消息表（每个会话一张）

每个会话（私聊/群聊）对应一张独立的消息表，表名格式为 `Msg_<MD5>`，MD5 由会话用户名哈希而来。

| 列名 | 类型 | 说明 |
|------|------|------|
| `local_id` | INTEGER PK | 本地自增消息 ID |
| `server_id` | INTEGER | 服务端消息 ID |
| `local_type` | INTEGER | 消息类型（见 §3） |
| `sort_seq` | INTEGER | 排序序号（= create_time × 1000） |
| `real_sender_id` | INTEGER | 发送者，对应 `Name2Id.rowid` |
| `create_time` | INTEGER | 消息时间（Unix 时间戳，秒） |
| `status` | INTEGER | 消息状态 |
| `upload_status` | INTEGER | 上传状态 |
| `download_status` | INTEGER | 下载状态 |
| `server_seq` | INTEGER | 服务端序列号 |
| `origin_source` | INTEGER | 来源方向 |
| `source` | TEXT/BLOB | 消息元数据（XML 格式） |
| `message_content` | TEXT/BLOB | 消息正文 |
| `compress_content` | TEXT | 压缩内容（备用） |
| `packed_info_data` | BLOB | 打包附加信息 |
| `WCDB_CT_message_content` | INTEGER | message_content 压缩标记 |
| `WCDB_CT_source` | INTEGER | source 压缩标记 |

#### 关键字段详解

**`status` 消息状态：**

| 值 | 含义 |
|----|------|
| 2 | 自己发送的消息 |
| 3 | 收到的消息 |

**`origin_source` 来源方向：**

| 值 | 含义 |
|----|------|
| 1 | 自己 |
| 2 | 对方 |

**`WCDB_CT_*` 压缩标记：**

| 值 | 含义 |
|----|------|
| 0 | 明文，不压缩 |
| 4 | **zstd 压缩**（`\x28\xb5\x2f\xfd` magic bytes） |

> 纯文本消息 `message_content` 通常为明文 (CT=0)，图片/语音/视频/表情等富媒体消息内容为 XML 格式并用 zstd 压缩存储 (CT=4)。`source` 字段也同理。

### 2.2 Name2Id — 用户名 ↔ ID 映射

| 列名 | 类型 | 说明 |
|------|------|------|
| `user_name` | TEXT PK | 微信 ID 或群号 |
| `is_session` | INTEGER | 1=会话，0=非会话 |

- **rowid** 即为消息表中 `real_sender_id` 的值
- `rowid=1` → 空字符串（系统）
- `rowid=2` → `wxid_zdtd5hn0taqp31`（当前登录用户自己）
- 群聊格式：`12345678@chatroom`
- 私聊格式：`wxid_xxxxxxxxx`

共 174 行，其中 49 个会话 (is_session=1)，2 个非会话。

### 2.3 其他辅助表

| 表名 | 行数 | 说明 |
|------|------|------|
| `DeleteInfo` | 0 | 已删除的消息记录（chat_name_id, delete_table_name） |
| `DeleteResInfo` | 0 | 已删除资源路径 |
| `HistoryAddMsgInfo` | 0 | 历史消息增量同步 |
| `HistorySysMsgInfo` | 0 | 历史系统消息 |
| `SendInfo` | 0 | 发送中的消息队列 |
| `TimeStamp` | 1 | 时间戳记录（1770564118） |
| `sqlite_sequence` | 49 | SQLite 自增序列（每个 Msg 表一条） |
| `wcdb_builtin_compression_record` | 0 | WCDB 压缩配置 |

---

## 3. 消息类型 (local_type)

### 3.1 基础类型（低 16 位）

| local_type | 含义 | message_content 格式 |
|------------|------|---------------------|
| 1 | 文本消息 | 纯文本，明文存储 |
| 3 | 图片 | XML（CDN 地址、AES key、缩略图尺寸） |
| 34 | 语音 | XML（CDN 地址、AES key、时长、格式） |
| 43 | 视频 | XML（CDN 地址、AES key、缩略图） |
| 47 | 表情包 | XML（emoji CDN、MD5、产品 ID） |
| 50 | 音视频通话 (VOIP) | XML（通话类型、时长、房间 ID） |
| 10000 | 系统消息 | XML（撤回、提醒、红包等） |

### 3.2 复合类型（高位编码）

`local_type` 大于 65535 时，格式为复合编码：

```
local_type = (app_msg_type << 32) | base_type
```

其中 `base_type = 0x31 (49)` 表示"应用消息"（appmsg），`app_msg_type` 区分子类型：

| local_type | 十六进制 | app_msg_type | 含义 |
|------------|---------|--------------|------|
| 17179869233 | 0x0400000031 | 4 | 链接分享（B 站视频等） |
| 21474836529 | 0x0500000031 | 5 | 公众号文章 |
| 81604378673 | 0x1300000031 | 19 | 聊天记录转发（合并转发） |
| 244813135921 | 0x3900000031 | 57 | 引用回复 |
| 266287972401 | 0x3E00000031 | 62 | 拍一拍 |

---

## 4. 消息内容示例

### 4.1 文本消息 (type=1)

```
message_content: "明天再和他约了 今天有点冷"
WCDB_CT_message_content: 0  (明文)
```

### 4.2 图片消息 (type=3)

```xml
<!-- WCDB_CT_message_content: 4 (zstd 解压后) -->
<msg>
  <img aeskey="2f8939ede48f0b12784f46d0b2a15b9d"
       encryver="1"
       cdnthumburl="3057020100..."
       cdnthumblength="4012"
       cdnthumbheight="432"
       cdnthumbwidth="242" />
</msg>
```

### 4.3 语音消息 (type=34)

```xml
<msg>
  <voicemsg endflag="1" voiceformat="4"
            voicelength="26605" length="47669"
            aeskey="d60c8918ca9e13a06b4ed344eae8b7a3"
            voiceurl="3052020100..." />
</msg>
```

### 4.4 表情包 (type=47)

```xml
<msg>
  <emoji fromusername="wxid_xxx" tousername="wxid_yyy"
         type="1" md5="8c9e5a88b299801cad1d0e413bfa6cdc"
         len="196589" productid="com.tencent.xin.emoticon..."
         cdnurl="http://wxapp.tc.qq.com/275/..." />
</msg>
```

### 4.5 引用回复 (type=0x3900000031)

```xml
<msg>
  <appmsg>
    <title>是的，但是有的人就是要买这个气氛</title>
    <type>57</type>
    <!-- 包含被引用的原消息 -->
  </appmsg>
</msg>
```

### 4.6 系统消息 (type=10000)

```xml
<sysmsg type="revokemsg">
  <revokemsg>
    <content>你撤回了一条消息</content>
  </revokemsg>
</sysmsg>
```

---

## 5. source 字段

XML 格式的消息元数据，zstd 压缩存储 (CT=4)，解压后示例：

```xml
<msgsource>
  <pua>1</pua>
  <eggIncluded>1</eggIncluded>
  <signature>N0_V1_Bj0g4/E0|v1_tEe5MjTh</signature>
  <tmp_node>
    <publisher-id></publisher-id>
  </tmp_node>
</msgsource>
```

包含消息签名、发布者信息等内部元数据。

---

## 6. 数据关系图

```
Name2Id (174 行)
  ├─ rowid=1 → ""          (系统)
  ├─ rowid=2 → "wxid_zdtd5hn0taqp31"  (自己)
  ├─ rowid=3 → "48301567450@chatroom"  (群聊)
  ├─ rowid=6 → "wxid_5woj4jpi7msh22"  (私聊)
  └─ ...
       │
       │  real_sender_id 引用 rowid
       ▼
Msg_<hash> (49 张表)
  ├─ Msg_ff44416f... → 1033 条（最大会话）
  ├─ Msg_987f8a54... → 171 条
  ├─ Msg_71ce4987... → 167 条
  └─ ...
```

---

## 7. 消息统计（最大会话）

```
会话: Msg_ff44416f113e53b15c00bdbc5ffc2173 (1033 条)
发送者:
  wxid_4ksh42oadu3y12  → 589 条 (57%)
  wxid_zdtd5hn0taqp31  → 433 条 (42%) [自己]
  系统                  →  11 条 (1%)

消息类型:
  文本       729 条 (70.6%)
  表情包      83 条 (8.0%)
  语音        70 条 (6.8%)
  引用回复    70 条 (6.8%)
  图片        40 条 (3.9%)
  拍一拍      21 条 (2.0%)
  系统消息    11 条 (1.1%)
  链接分享     6 条
  VOIP 通话    2 条
  视频         1 条
```

---

## 8. bizchat.db 内部结构

> 密钥: `d23bbaa27983d80f2ec64f47bc343119497224356b6d89d85e351d9e860f0bfb`  
> 加密方式: SQLCipher 4 (cipher_compatibility = 4)  
> **注意: 所有表当前均为空（0 行数据），该用户未使用企业微信聊天功能**

共 **4 张表**，用于存储企业微信（BizChat）相关数据。

### 8.1 chat_group — 企业聊天群组

| 列名 | 类型 | 说明 |
|------|------|------|
| `id` | INTEGER PK | 群组 ID（自增） |
| `group_id` | TEXT | 群组标识 |
| `brand_user_name` | TEXT | 关联的企业号用户名 |
| `type` | INTEGER | 群组类型 |
| `version` | INTEGER | 版本号 |
| `bit_flag` | INTEGER | 位标志 |
| `max_member_count` | INTEGER | 最大成员数 |
| `chat_name` | TEXT | 群聊名称 |
| `owner_id` | TEXT | 群主 ID |
| `head_img_url` | TEXT | 群头像 URL |
| `user_list` | TEXT | 成员列表 |
| `add_member_url` | TEXT | 添加成员 URL |
| `reserved0` | INTEGER | 保留字段 |
| `reserved1` | INTEGER | 保留字段 |
| `reserved2` | TEXT | 保留字段 |
| `reserved3` | TEXT | 保留字段 |

### 8.2 user_info — 企业号用户信息

| 列名 | 类型 | 说明 |
|------|------|------|
| `id` | INTEGER PK | 用户 ID（自增） |
| `user_id` | TEXT | 用户标识 |
| `brand_user_name` | TEXT | 企业号用户名 |
| `user_name` | TEXT | 用户显示名 |
| `version` | INTEGER | 版本号 |
| `bit_flag` | INTEGER | 位标志 |
| `head_img_url` | TEXT | 头像 URL |
| `profile_url` | TEXT | 个人资料 URL |
| `add_member_url` | TEXT | 添加成员 URL |
| `reserved0` | INTEGER | 保留字段 |
| `reserved1` | INTEGER | 保留字段 |
| `reserved2` | TEXT | 保留字段 |
| `reserved3` | TEXT | 保留字段 |

### 8.3 my_user_info — 当前用户的企业号信息

| 列名 | 类型 | 说明 |
|------|------|------|
| `brand_user_name` | TEXT PK | 企业号用户名（唯一） |
| `user_id` | TEXT | 当前用户 ID |

### 8.4 name2id — 用户名映射

| 列名 | 类型 | 说明 |
|------|------|------|
| `username` | TEXT PK | 用户名标识（唯一） |

> **与 message_0.db 的 `Name2Id` 不同**：bizchat 的 `name2id` 只有一列 `username`，没有 `is_session` 字段。

### 8.5 数据关系

```
my_user_info (当前用户企业号身份)
  brand_user_name ──┐
                    ├──→ user_info.brand_user_name (企业号下的用户信息)
                    └──→ chat_group.brand_user_name (企业号下的群聊)

chat_group
  owner_id ──→ user_info.user_id (群主)
  user_list → JSON/逗号分隔的成员 user_id 列表
```

---

## 9. biz_message_0.db 内部结构

> 密钥: `b037869a62da6407ab5d7ea37a611b5d2bfb4edce26675e27ee3cfd114dc6528`  
> 加密方式: SQLCipher 4 (cipher_compatibility = 4)  
> 用途: **公众号消息存储**

共 **160 个表**：154 个 `Msg_<hash>` 消息子表 + 6 个辅助表。

### 9.1 总体统计

| 项目 | 数值 |
|------|------|
| Msg 子表 | 154 张（全部非空） |
| 总消息数 | 1943 条 |
| Name2Id 条目 | 157 行 |
| 公众号 (gh_) | 150 个 |
| 个人号 (wxid_) | 3 个 |

### 9.2 Msg_\<hash\> — 公众号消息表

表结构与 `message_0.db` 的 Msg 表**完全一致**：

| 列名 | 类型 | 说明 |
|------|------|------|
| `local_id` | INTEGER PK | 本地自增消息 ID |
| `server_id` | INTEGER | 服务端消息 ID |
| `local_type` | INTEGER | 消息类型 |
| `sort_seq` | INTEGER | 排序序号 |
| `real_sender_id` | INTEGER | 发送者，对应 `Name2Id.rowid` |
| `create_time` | INTEGER | 创建时间（Unix 时间戳） |
| `status` | INTEGER | 消息状态 |
| `upload_status` | INTEGER | 上传状态 |
| `download_status` | INTEGER | 下载状态 |
| `server_seq` | INTEGER | 服务端序列号 |
| `origin_source` | INTEGER | 来源方向 |
| `source` | TEXT/BLOB | 消息元数据 XML |
| `message_content` | TEXT/BLOB | 消息正文 |
| `compress_content` | TEXT | 压缩内容 |
| `packed_info_data` | BLOB | 打包附加信息 |
| `WCDB_CT_message_content` | INTEGER | 压缩标记 |
| `WCDB_CT_source` | INTEGER | 压缩标记 |

### 9.3 消息类型分布

| local_type | 十六进制 | 含义 | 数量 | 占比 |
|------------|---------|------|------|------|
| 1 | 0x1 | 文本消息 | 1190 | 61.2% |
| 21474836529 | 0x500000031 | 公众号文章 (appmsg type=5) | 753 | 38.8% |

> 与 `message_0.db` 不同，公众号消息库只有两种类型：**纯文本**和**公众号文章链接**。

### 9.4 WCDB 压缩

| WCDB_CT_message_content | 数量 | 说明 |
|--------------------------|------|------|
| 0 | 6 | 明文存储 |
| 4 | 1937 | zstd 压缩（99.7%） |

> 绝大多数消息（包括文本）都采用 zstd 压缩存储，与 `message_0.db` 中文本通常明文存储不同。

### 9.5 Name2Id — 公众号/用户映射

结构与 `message_0.db` 一致：

| 列名 | 类型 | 说明 |
|------|------|------|
| `user_name` | TEXT PK | 公众号原始 ID 或微信 ID |
| `is_session` | INTEGER | 1=会话 |

共 157 行，其中：
- `gh_xxx` (公众号): **150 个**
- `wxid_xxx` (个人号): 3 个（含当前用户 `wxid_zdtd5hn0taqp31`）
- 其他: 4 个

### 9.6 辅助表

| 表名 | 行数 | 说明 |
|------|------|------|
| `DeleteInfo` | 0 | 已删除消息记录 |
| `DeleteResInfo` | 0 | 已删除资源 |
| `Name2Id` | 157 | 公众号/用户映射 |
| `TimeStamp` | 1 | 时间戳记录（1770564114） |
| `sqlite_sequence` | 154 | 自增序列 |
| `wcdb_builtin_compression_record` | 0 | WCDB 压缩配置 |

### 9.7 消息量 Top 10 公众号

| 排名 | 消息表 | 消息数 |
|------|--------|--------|
| 1 | Msg_5578a36daa2a4ef275600813997afb59 | 910 条 |
| 2 | Msg_f184616a87e1da0c4cbd0989fd148faa | 273 条 |
| 3 | Msg_fce22bc16ba933bf9d90743b50699f12 | 76 条 |
| 4 | Msg_0e39b9bce1077f2ca37486a5392dfb45 | 71 条 |
| 5 | Msg_211e4bc952c1e75577b98b3ab947ac4d | 68 条 |
| 6 | Msg_91e3eac6961a0c32763df9dc944c63ed | 45 条 |
| 7 | Msg_c156d1a38aa55c427b37494e8f31104c | 34 条 |
| 8 | Msg_34ef598f85190ae340006e11739796b8 | 21 条 |
| 9 | Msg_c6229dc2529c6c8f9745215af43f32f8 | 8 条 |
| 10 | Msg_14009dfee7ecd1cfbad8fa9befbe8da8 | 7 条 |

### 9.8 公众号文章消息示例 (type=0x500000031)

```xml
<!-- WCDB_CT_message_content: 4 (zstd 解压后) -->
<msg>
  <appmsg appid="" sdkver="0">
    <title><![CDATA[2026襄阳各大商场春节营业时间！]]></title>
    <des><![CDATA[快来看~]]></des>
    <type>5</type>
    <showtype>1</showtype>
    <url><![CDATA[http://mp.weixin.qq.com/s?__biz=MzIwODY1NDc4Mw==&mid=...]]></url>
    <!-- 包含文章标题、摘要、URL、封面图等 -->
  </appmsg>
</msg>
```

### 9.9 与 message_0.db 的对比

| 特征 | message_0.db | biz_message_0.db |
|------|-------------|-----------------|
| 用途 | 私聊/群聊消息 | 公众号消息 |
| Msg 子表数 | 49 | 154 |
| 总消息数 | ~数千 | 1943 |
| 消息类型 | 丰富（文本/图片/语音/视频等） | 仅文本 + 公众号文章 |
| Name2Id 主体 | wxid_（个人）+ chatroom（群） | gh_（公众号） |
| 压缩比例 | 部分压缩（富媒体压缩，文本明文） | 几乎全部压缩（99.7%） |
| 表结构 | 完全一致 | 完全一致 |

---

## 10. contact.db 内部结构

> 密钥: `145635e5ac6d4682387a9611203a9ee0abe64b56359282b04523e41582703951`  
> 加密方式: SQLCipher 4 (cipher_compatibility = 4)  
> 用途: **联系人、群聊、公众号信息存储**

共 **16 张表**。

### 10.1 总体统计

| 表名 | 行数 | 说明 |
|------|------|------|
| `contact` | 7632 | 所有联系人（个人/群聊/公众号/系统号） |
| `name2id` | 7632 | 用户名映射（与 contact 一一对应） |
| `chatroom_member` | 7129 | 群聊成员关系（53 群，6301 去重成员） |
| `chat_room` | 53 | 群聊基本信息 |
| `chat_room_info_detail` | 53 | 群聊详情（公告等） |
| `biz_info` | 884 | 公众号/服务号扩展信息 |
| `contact_label` | 13 | 联系人标签定义 |
| `openim_wording` | 47 | OpenIM 文案 |
| `ticket_info` | 4 | 加好友验证票据 |
| `openim_acct_type` | 1 | OpenIM 账号类型 |
| `openim_appid` | 1 | OpenIM 应用 ID |
| `encrypt_name2id` | 0 | 加密用户名映射（空） |
| `oplog` | 0 | 操作日志（空） |
| `stranger` | 0 | 陌生人（空） |
| `stranger_ticket_info` | 0 | 陌生人票据（空） |

### 10.2 contact — 联系人主表

| 列名 | 类型 | 说明 |
|------|------|------|
| `id` | INTEGER PK | 联系人 ID（自增） |
| `username` | TEXT | 微信 ID / gh_ 公众号 / chatroom 群号 |
| `local_type` | INTEGER | 联系人类型（见下） |
| `alias` | TEXT | 微信号（用户自定义） |
| `encrypt_username` | TEXT | 加密用户名（v3_ 开头） |
| `flag` | INTEGER | 标志位 |
| `delete_flag` | INTEGER | 删除标记（0=正常, 1=已删除） |
| `verify_flag` | INTEGER | 认证标志 |
| `remark` | TEXT | 备注名 |
| `remark_quan_pin` | TEXT | 备注全拼 |
| `remark_pin_yin_initial` | TEXT | 备注拼音首字母 |
| `nick_name` | TEXT | 昵称 |
| `pin_yin_initial` | TEXT | 昵称拼音首字母 |
| `quan_pin` | TEXT | 昵称全拼 |
| `big_head_url` | TEXT | 大头像 URL |
| `small_head_url` | TEXT | 小头像 URL |
| `head_img_md5` | TEXT | 头像 MD5 |
| `chat_room_notify` | INTEGER | 群聊免打扰 |
| `is_in_chat_room` | INTEGER | 是否在群聊中 |
| `description` | TEXT | 描述/签名 |
| `extra_buffer` | BLOB | 扩展数据（二进制） |
| `chat_room_type` | INTEGER | 群聊类型 |

**索引**: `contact_local_type` (local_type)

#### contact.local_type 分布

| local_type | 数量 | 含义 |
|------------|------|------|
| 0 | 8 | 系统账号（notifymessage、filehelper 等） |
| 1 | 1426 | 公众号/服务号 |
| 2 | 42 | 群聊 |
| 3 | 6071 | 个人联系人 |
| 5 | 59 | 企业微信联系人 |
| 6 | 26 | OpenIM 联系人 |

#### contact.verify_flag 分布

| verify_flag | 数量 | 含义 |
|-------------|------|------|
| 0 | 6748 | 普通用户/群聊 |
| 8 | 545 | 认证公众号 |
| 24 | 315 | 认证服务号 |
| 其他 | 24 | 企业号等特殊认证 |

### 10.3 chat_room — 群聊信息

| 列名 | 类型 | 说明 |
|------|------|------|
| `id` | INTEGER PK | 群 ID（自增） |
| `username` | TEXT | 群号（如 `12345@chatroom`） |
| `owner` | TEXT | 群主微信 ID |
| `ext_buffer` | BLOB | 扩展数据 |

### 10.4 chat_room_info_detail — 群聊详情

| 列名 | 类型 | 说明 |
|------|------|------|
| `room_id_` | INTEGER PK | 群 ID（对应 chat_room.id） |
| `username_` | TEXT | 群号 |
| `announcement_` | TEXT | 群公告 |
| `announcement_editor_` | TEXT | 公告编辑者 |
| `announcement_publish_time_` | INTEGER | 公告发布时间 |
| `chat_room_status_` | INTEGER | 群状态 |
| `xml_announcement_` | TEXT | XML 格式公告 |
| `ext_buffer_` | BLOB | 扩展数据 |

### 10.5 chatroom_member — 群成员关系

| 列名 | 类型 | 说明 |
|------|------|------|
| `room_id` | INTEGER | 群 ID（对应 chat_room.id） |
| `member_id` | INTEGER | 成员 ID（对应 contact.id） |

**索引**: `chatroom_member_room_id`, `chatroom_member_member_id`  
**唯一约束**: (room_id, member_id)

共 7129 行，53 个群，6301 个去重成员。最大群 500 人。

### 10.6 biz_info — 公众号扩展信息

| 列名 | 类型 | 说明 |
|------|------|------|
| `id` | INTEGER PK | ID（自增） |
| `username` | TEXT | 公众号原始 ID（gh_xxx） |
| `type` | INTEGER | 公众号类型（0/1/2/3） |
| `accept_type` | INTEGER | 接受类型 |
| `child_type` | INTEGER | 子类型 |
| `version` | INTEGER | 版本号 |
| `external_info` | TEXT | 外部信息（JSON，含菜单、交互模式等） |
| `brand_info` | TEXT | 品牌信息 |
| `brand_icon_url` | TEXT | 品牌图标 URL |
| `brand_list` | TEXT | 品牌列表 |
| `brand_flag` | INTEGER | 品牌标志 |
| `belong` | TEXT | 所属主体 |
| `ext_buffer` | BLOB | 扩展数据 |

共 884 行。type 分布: type=0(271), type=1(232), type=2(2), type=3(379)。

### 10.7 contact_label — 联系人标签

| 列名 | 类型 | 说明 |
|------|------|------|
| `label_id_` | INTEGER PK | 标签 ID |
| `label_name_` | TEXT | 标签名称 |
| `sort_order_` | INTEGER | 排序序号 |

共 13 个标签: 家人、研究生同学、腾讯、初中、一面之缘、微商、网友、大学、朋友、高中、小学、二度关系等。

### 10.8 name2id — 用户名映射

| 列名 | 类型 | 说明 |
|------|------|------|
| `username` | TEXT PK | 用户名标识 |

共 7632 行，与 contact 表一一对应。`rowid` 用于其他表引用。

### 10.9 数据关系

```
contact (7632 行)
  id ──→ chatroom_member.member_id (群成员)
  id ──→ chat_room.id → chatroom_member.room_id (群聊)
  username ──→ biz_info.username (公众号扩展信息)
  id ──→ ticket_info.id (验证票据)

chat_room (53 行)
  id ──→ chat_room_info_detail.room_id_ (群详情)
  id ──→ chatroom_member.room_id (群成员列表)

name2id (7632 行)
  rowid 与 contact.id 对应
```

---

## 11. session.db 内部结构

> 密钥: `8a1970ceda29484584e92c52ceb88dbcc853c2d889ec8623216ac7c220757c39`  
> 加密方式: SQLCipher 4 (cipher_compatibility = 4)  
> 用途: **会话列表与未读消息管理**

共 **6 张表**。

### 11.1 总体统计

| 表名 | 行数 | 说明 |
|------|------|------|
| `SessionTable` | 268 | 所有会话（私聊/群聊/公众号/系统） |
| `Name2Id` | 208 | 用户名映射 |
| `SessionUnreadListTable_1` | 1854 | 未读消息明细 |
| `SessionUnreadStatTable_1` | 0 | 未读统计（空） |
| `SessionDeleteTable` | 0 | 已删除会话（空） |
| `SessionNoContactInfoTable` | 0 | 无联系人信息的会话（空） |

### 11.2 SessionTable — 会话列表

| 列名 | 类型 | 说明 |
|------|------|------|
| `username` | TEXT PK | 会话标识（微信 ID / 群号 / 公众号） |
| `type` | INTEGER | 会话类型（全部为 0） |
| `unread_count` | INTEGER | 未读消息数 |
| `unread_first_msg_srv_id` | INTEGER | 首条未读消息服务端 ID |
| `unread_first_pat_msg_local_id` | INTEGER | 首条未读拍一拍本地 ID |
| `unread_first_pat_msg_sort_seq` | INTEGER | 首条未读拍一拍排序序号 |
| `is_hidden` | INTEGER | 是否隐藏（全部为 0） |
| `summary` | TEXT | 最后一条消息摘要 |
| `draft` | TEXT | 草稿内容 |
| `status` | INTEGER | 会话状态 |
| `last_timestamp` | INTEGER | 最后消息时间（Unix 时间戳） |
| `sort_timestamp` | INTEGER | 排序时间戳 |
| `last_clear_unread_timestamp` | INTEGER | 最后清除未读时间 |
| `last_msg_locald_id` | INTEGER | 最后消息本地 ID |
| `last_msg_type` | INTEGER | 最后消息类型 |
| `last_msg_sub_type` | INTEGER | 最后消息子类型 |
| `last_msg_sender` | TEXT | 最后消息发送者 |
| `last_sender_display_name` | TEXT | 发送者显示名 |
| `last_msg_ext_type` | INTEGER | 最后消息扩展类型 |

**索引**: `SessionTable_TYPE` (type), `SessionTable_LSENDER` (last_msg_sender)

#### last_msg_type 分布

| last_msg_type | 数量 | 含义 |
|---------------|------|------|
| 49 | 163 | 应用消息（公众号文章等） |
| 0 | 56 | 无消息 |
| 1 | 29 | 文本消息 |
| 3 | 9 | 图片 |
| 47 | 5 | 表情包 |
| 10000 | 3 | 系统消息 |
| 50 | 2 | 音视频通话 |
| 34 | 1 | 语音 |

### 11.3 Name2Id — 用户名映射

| 列名 | 类型 | 说明 |
|------|------|------|
| `user_name` | TEXT PK | 用户名标识 |

共 208 行。`rowid` 被 `SessionUnreadListTable_1.username_id` 引用。  
含 OpenIM 用户（`@openim` 后缀）、公众号、群聊、个人号等。

### 11.4 SessionUnreadListTable_1 — 未读消息明细

| 列名 | 类型 | 说明 |
|------|------|------|
| `username_id` | INTEGER PK | 会话 ID（对应 Name2Id.rowid） |
| `server_id` | INTEGER PK | 消息服务端 ID |
| `create_time` | INTEGER | 消息创建时间 |

**索引**: (username_id, create_time)  
**唯一约束**: (username_id, server_id)

共 1854 条未读，涉及 105 个会话。

### 11.5 辅助表

| 表名 | 列 | 说明 |
|------|-----|------|
| `SessionDeleteTable` | username TEXT PK, delete_time INTEGER | 已删除的会话 |
| `SessionNoContactInfoTable` | username TEXT PK, session_title TEXT | 无联系人信息的会话标题 |
| `SessionUnreadStatTable_1` | username_id INTEGER PK, unread_stat INTEGER | 未读统计 |

### 11.6 未读 Top 10

| 会话 | 未读数 | 最后消息摘要 |
|------|--------|-------------|
| gh_7134c254c849 | 975 | 告警策略（信安基础服务） |
| gh_3dbaad486f7f | 291 | 告警策略（信安基础服务） |
| @placeholder_foldgroup | 226 | 折叠群组 |
| gh_c5f856535378 | 177 | 告警策略（信安基础服务） |
| 48301567450@chatroom | 117 | seedance太🐮了 |
| 57167887777@chatroom | 92 | 群聊 |
| brandsessionholder | 70 | 公众号消息聚合 |
| 48732353833@chatroom | 24 | 群聊 |
| 50072096593@chatroom | 15 | 群聊 |
| gh_363b924965e9 | 12 | 公众号 |

### 11.7 与其他数据库的关联

```
SessionTable.username ──→ contact.username (联系人信息)
                      ──→ message_0.db / biz_message_0.db 的 Name2Id.user_name (消息内容)

SessionUnreadListTable_1.username_id ──→ Name2Id.rowid (会话标识)
SessionUnreadListTable_1.server_id ──→ Msg_<hash>.server_id (具体消息)
```

---

## 12. 备注

- **压缩算法**: WCDB 使用 **zstd** 而非 zlib，magic bytes 为 `\x28\xb5\x2f\xfd`
- **密钥独立**: 每个数据库使用不同密钥（已确认 message_0.db、bizchat.db、biz_message_0.db、contact.db、session.db 五个密钥各不相同）
- **其他数据库**: 8 个数据库尚未解开，可能每个数据库使用独立密钥
- **密钥来源**: 从微信进程内存中提取，存储于匿名 mmap 区域（glibc 子线程 arena）
