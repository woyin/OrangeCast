# ADR-0007：从内容模型移除租户所有权

CloudWisePod 仅保留一份单例 Owner 凭据用于实例访问控制，Podcast、Candidate、Source、EvidenceAudio、Transcript、KnowledgeCard、ProcessingJob 等内容数据不再携带 `user_id`，业务接口与查询也不再接受用户作用域；现有数据通过兼容迁移归入当前实例。该决策承担一次性迁移与代码改造成本，以消除不会使用的多租户复杂度，并使数据模型真实表达单 Owner 产品边界。
