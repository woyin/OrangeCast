# ADR-0004：知识笔记单向沉淀到个人知识库

CloudWisePod 作为证据库，保存音频、转录稿、引用和 AI 生成的知识卡片；Owner 编辑后的知识笔记以外部个人知识库为权威版本。两者通过确定性的 Markdown 单向沉淀，首版由浏览器一键下载带 Citation 链接的文件，不直接写入 Obsidian Vault，也不开发插件或接入 Git、WebDAV 双向同步。CloudWisePod 不承担通用笔记编辑器职责，以避免版本冲突和持续同步成本，同时接受沉淀后的编辑不会回写证据库。
