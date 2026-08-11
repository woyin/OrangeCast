# 0020 质量门禁：覆盖率 ≥95% 与导出符号注释

为 CloudWisePod 引入可度量的结构门槛：`scripts/cover-gate.sh` 断言每个有测试的包语句覆盖率 ≥95%，`scripts/lint.sh`（revive `exported` 规则）强制每个导出符号都有以其符号名开头的 Godoc 注释。二者均接入 CI（`make cover-gate` / `make lint`）与本地 `Makefile`。

**动机**：重构目标的三根轴中，结构条理性难以量化，而覆盖率为我们提供可审计的落地抓手。项目原本就高覆盖（多数组 98–100%），仅有 `backup`(91%) 一个洼地，因此设 95% 地板是对现状的低扰动约束，而非抬高到压垮性门槛。

**关键取舍**：
- **`backup` 用测试缝而非豁免**：`backup.Create` 的 tar/gzip 写入错误分支无法通过公开 API 触达。为保持门禁"诚实"（不设豁免）、而非给 94%→95% 以任何回旋余地，我们选择在 `internal/backup/backup.go` 注入三个包级工厂变量（`newTarWriter`/`newGzipWriter`/`openArchiveSrc`）作为测试缝，默认实现即标准库、零行为变更，测试中替换为伪造实现以驱动写失败分支，使 `backup` 达 95.6%。这是"宁可加一个微小的测试缝，也不在门禁上开口子"的取舍。
- **`internal/models` 豁免**：它是纯类型/常量声明包，无可执行语句，0% 覆盖是工具假象，故豁免并在脚本中以注释注明。
- **revive 只启用 `exported` + 关闭 stutter**：`exported` 规则内置了"注释以符号名开头"的语义，正是注释覆盖率的目标；同时用 `disableStutteringCheck` 关掉对 `provider.ProviderBundle` 这类合法命名的重复前缀告警，避免噪音掩盖真正的注释缺失。
- **注释风格延续中文 `// 符号名 说明`**：与既有 `models.go` 注释保持一致，而非引入英文 Godoc。

**后果**：新功能必须附带测试使所属包 ≥95%（否则 CI 红）；新增导出符号必须带正确前缀注释。未来 reviewer / CI 首次见到 `backup.go` 顶部的工厂变量时，应意识到这是受 ADR-0020 约束的测试缝。
