# V04 文档终校记录

日期：2026-10-02。从仓库根目录执行以下小型校验，未重跑生产测试、未读取密钥值、未修改主任务状态。此次证据仅证明下列文档当前本地链接、目标文件与 Markdown 标题锚点有效，并核对说明与冻结源码契约；不证明真实模型质量、实体手机或生产部署通过。

## 核对范围

- [README](../../../../README.md)
- [第四轮使用说明](../../../personal-learning-v4.md)
- [第四轮配置示例](../../../configuration-v4.example.md)
- [生产部署指南](../../../production-deployment.md)

人工契约复核：真实标题选择导出、CWPForms 预览确认、404/409 就地保稿和切页守卫；四用途默认自动及可选计划暂停；理解保存/选择/解决的区分；专用离线壳真实区间、独立进度及旧格式只读复制；语义报告准入和有效启用状态；CLI 阶段预算/CAS/receipt；未知不伪记零、已知缓存不重付费。自建20条笔记不能代替真实个人笔记质量；真实调用 usage 1428/983、实际模型不匹配而拒绝，不等于审校或人评通过。

## 可复验命令

```sh
python3 - <<'PY'
from pathlib import Path
import hashlib,json,re
files=list(map(Path,['README.md','docs/personal-learning-v4.md','docs/configuration-v4.example.md','docs/production-deployment.md']))
errors=[];n=0
for f in files:
 for dest in re.findall(r'\]\(([^)]+)\)',f.read_text()):
  if '://' in dest:continue
  path,_,anchor=dest.partition('#');target=f.parent/path if path else f;n+=1
  if not target.exists():errors.append({'source':str(f),'target':dest,'error':'missing file'});continue
  if anchor and target.suffix=='.md':
   headings=[re.sub(r'[^\w\-\s]','',x.lower()).strip().replace(' ','-') for x in re.findall(r'^#+\s+(.+)$',target.read_text(),re.M)]
   if anchor not in headings and f'id="{anchor}"' not in target.read_text():errors.append({'source':str(f),'target':dest,'error':'missing anchor'})
print(json.dumps({'documents':len(files),'local_links_and_anchors':n,'errors':errors},ensure_ascii=False))
for f in files:print(hashlib.sha256(f.read_bytes()).hexdigest(),str(f))
raise SystemExit(bool(errors))
PY
git diff --check
```

## 实际输出

```text
{"documents": 4, "local_links_and_anchors": 32, "errors": []}
ad9b183a2281a22cde8e05e58cfa9b9c3b9db353bef7562d25c0567fb8ee9c0a README.md
ea0ebb9ed06c5d2987274f64bef65bed886deeecc1ef0061162e05ff0bf3d947 docs/personal-learning-v4.md
775147c7f7457b7723bd9754f7905bea118c1f12bfe76f13f619d3635d925beb docs/configuration-v4.example.md
681d065fc7b55bc7d6741c0bcfaf3257ae1d40e0f4bb30708fea9cd82bb04fc4 docs/production-deployment.md
```

Python 退出码 0。`git diff --check` 无输出、退出码 0。SHA256 固定本次被检查文件；后续文档改变时应重新执行，不沿用本记录推断新文件通过。校验只遍历四文档的内联 Markdown 链接，跳过外部 URL，不测试外站可达性或浏览器渲染。

结论：V04 文档技术终校通过。真实质量、手机、实际部署验收仍按[验证记录](../../plans/2026-10-01-personal-learning-and-knowledge-articles-v4-validation.md)分别登记。
