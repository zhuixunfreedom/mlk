# 真实目标在 server/Makefile。根目录只转发，server 迭代独立管理。
MAKEFLAGS += --no-print-directory
.PHONY: all
all:
	@$(MAKE) -C server help
%:
	@$(MAKE) -C server $@
