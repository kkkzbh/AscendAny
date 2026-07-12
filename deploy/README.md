# AscendAny deployment

AscendAny v2 的唯一生产部署、权限与验收入口是 [deploy/v2/README.md](v2/README.md)。

本地备份与隔离恢复使用仓库根目录的
`tools/run-v2-backup-restore-podman-rehearsal.sh`。默认输入是已提交的脱敏 Pintia
v2 fixture；`--snapshot` 只接受绝对 canonical 受保护文件，并在创建数据库前通过
production Go validator 校验。

v2 使用 Go binaries、独立 systemd identity、PostgreSQL capability roles、systemd encrypted credentials 和 fresh `ascendany_v2` database。仓库不提供旧 Python API、旧数据库迁移、virtualenv 部署或 legacy DDL 执行入口。
