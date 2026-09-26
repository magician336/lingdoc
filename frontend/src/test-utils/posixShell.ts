import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

/**
 * 这台机器上可用的 **bash 兼容** POSIX shell 的路径；一台都找不到时返回 null。
 *
 * 为什么要逐个挑，而不是直接写 `bash` 或 `/bin/sh`：
 *
 *  - **Windows 上没有 `/bin/sh`。** Node 会把这个字符串原样交给 CreateProcess，
 *    拿到 ENOENT、`status === null`，于是「`null !== 0`」这种失败看着像被测代码
 *    坏了，其实只是那个路径不存在。
 *  - **Windows 的 PATH 上排在前面的 `bash` 常常是 WSL 的**
 *    （`C:\Windows\System32\bash.exe`）。它能启动、退出码也正常，但它读不到 `D:\`
 *    这样的盘符路径，还会把 `-c` 里那串命令重新解析一遍——`$HOME` 被展开、单引号
 *    被拆开。于是「source 一个脚本」变成「脚本不存在」，而「参数保持字面量」这条
 *    断言在它下面必然失败。
 *
 * 从 Git Bash 启动 `npm test` 和从 PowerShell 启动，PATH 不一样，选中的 shell 就
 * 不一样，同一条用例于是时红时绿——上面两种失败都是这么来的。所以候选不看名字，
 * 而是**逐个拿一条真实的盘符路径去试**：读不出来的（WSL）自然被挡掉。
 *
 * 探针用的是本文件自己的路径。它就在仓库里，与被测的那些脚本同盘，所以这个问题
 * 正是调用方要问的那个：「这个 shell 能不能读仓库里的绝对路径」。
 */
export function findPosixShell(): string | null {
  for (const candidate of candidates()) {
    if (isBashCompatible(candidate)) return candidate
  }
  return null
}

const helperPath = fileURLToPath(import.meta.url)

/**
 * 叫 bash 的排在前面：两位调用方都要 bash 语义（`--norc --noprofile`、`alias`）。
 * Windows 上把 Git 的安装位置排在裸名字前面，是因为裸 `bash` 很可能命中 WSL——
 * 探针虽然拦得住它，但先试确定的那一个，选出来的结果才不随 PATH 顺序漂移。
 */
function candidates(): string[] {
  const names = process.platform === 'win32'
    ? [
        process.env.SHELL,
        'C:\\Program Files\\Git\\bin\\bash.exe',
        'C:\\Program Files (x86)\\Git\\bin\\bash.exe',
        'bash',
        'sh',
      ]
    : [process.env.SHELL, 'bash', '/bin/sh']
  return names.filter((name): name is string => typeof name === 'string' && name.length > 0)
}

/**
 * 探针要求的正是调用方要的两件事，一次问完：
 *
 *  - `--norc --noprofile` 是 bash 独有的选项，用它把 dash 一类挡在外面
 *    （Ubuntu 上 `SHELL` 常常就是 `/bin/sh` → dash，而 `SandboxTerminal` 那条
 *    用例要的正是 bash 语义）；
 *  - `[ -f '盘符路径' ]` 问它读不读得到仓库里的绝对路径，把 WSL 的 bash 挡在外面。
 */
function isBashCompatible(shell: string): boolean {
  // 引号不能省：`C:\Program Files\...` 里有空格，裸着写探针会把路径拆成两段而误判。
  const probe = spawnSync(shell, ['--norc', '--noprofile', '-c', `[ -f '${helperPath}' ]`], {
    encoding: 'utf8',
  })
  return probe.status === 0
}
