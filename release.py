# !/usr/bin/env python3
import argparse
import logging
import os
import subprocess
import zipfile

# --- 配置部分 ---
PROJECT_NAME = 'mosdns-log'          # 项目名称
ENTRY_POINT = '../main.go' # 编译入口 (相对于 release 目录)
CONFIG_FILE = '../config.yaml'        # 配置文件路径 (相对于 release 目录)
RELEASE_DIR = './release'             # 输出目录
WEB_DIR = '../web'                    # 前端资源目录

# --- 编译目标 ---
# 只保留了 Linux AMD64 v3 版本
envs = [
    [['GOOS', 'linux'], ['GOARCH', 'amd64'], ['GOAMD64', 'v3']]
]

# --- 初始化参数 ---
parser = argparse.ArgumentParser()
parser.add_argument("-upx", action="store_true", help="Use UPX to compress binary")
args = parser.parse_args()

logger = logging.getLogger(__name__)

def go_build():
    logger.info(f'🚀 开始编译 {PROJECT_NAME} ...')

    # 检查配置文件是否存在
    if not os.path.exists(CONFIG_FILE):
        logger.warning(f"⚠️  未找到配置文件 {CONFIG_FILE}，打包时将使用默认配置或跳过。")

    for env in envs:
        os_env = os.environ.copy()
        
        # 构建文件名后缀
        s = PROJECT_NAME
        for pairs in env:
            os_env[pairs[0]] = pairs[1]
            if pairs[0] in ['GOOS', 'GOARCH']:
                s = s + '-' + pairs[1]
            elif pairs[0] == 'GOAMD64':
                 s = s + '-v3' # 标记 v3 版本
        
        # Windows 后缀
        if os_env.get('GOOS') == 'windows':
             s += '.exe'

        zip_filename = s + '.zip'
        if zip_filename.endswith('.exe.zip'): 
             zip_filename = zip_filename.replace('.exe.zip', '.zip')
             
        bin_filename = PROJECT_NAME
        if os_env.get('GOOS') == 'windows':
            bin_filename += '.exe'

        logger.info(f'🔨 Building: {zip_filename} ({os_env.get("GOOS")}/{os_env.get("GOARCH")})...')

        try:
            # 构造编译命令
            # -s -w: 去掉调试信息，减小体积
            # -trimpath: 移除文件系统路径信息
            cmd = f'go build -ldflags "-s -w" -trimpath -o {bin_filename} {ENTRY_POINT}'
            
            subprocess.check_call(cmd, shell=True, env=os_env)

            # UPX 压缩 (可选)
            if args.upx:
                try:
                    logger.info('   Compressing with UPX...')
                    subprocess.check_call(f'upx -9 -q {bin_filename}', shell=True, 
                                          stderr=subprocess.DEVNULL, stdout=subprocess.DEVNULL)
                except Exception:
                    logger.error('   UPX compression failed or not installed, skipping.')

            # 打包 zip
            with zipfile.ZipFile(zip_filename, mode='w', compression=zipfile.ZIP_DEFLATED, compresslevel=9) as zf:
                # 1. 写入二进制
                zf.write(bin_filename)
                
                # 2. 写入配置 (如果存在)
                if os.path.exists(CONFIG_FILE):
                    zf.write(CONFIG_FILE, 'config.yaml')

                # 3. 写入说明文档 (如果存在)
                if os.path.exists('../README.md'):
                    zf.write('../README.md', 'README.md')
                    
                # 4. 写入 Web 静态资源 (递归)
                if os.path.exists(WEB_DIR):
                     for root, dirs, files in os.walk(WEB_DIR):
                         for file in files:
                             # 原始路径: ../web/css/style.css
                             # 归档路径: web/css/style.css
                             file_path = os.path.join(root, file)
                             # 计算相对路径 remove ../
                             archive_path = os.path.relpath(file_path, start='..')
                             zf.write(file_path, archive_path)
                else:
                    logger.warning(f"⚠️  未找到 Web 目录 {WEB_DIR}，前端界面可能无法加载。")

            # 清理临时二进制文件
            if os.path.exists(bin_filename):
                os.remove(bin_filename)

            logger.info(f'✅ Success: {zip_filename}')

        except subprocess.CalledProcessError as e:
            logger.error(f'❌ Build failed: {e}')
        except Exception as e:
            logger.exception(f'❌ Unknown error: {e}')

if __name__ == '__main__':
    logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(message)s', datefmt='%H:%M:%S')

    if len(RELEASE_DIR) != 0:
        if not os.path.exists(RELEASE_DIR):
            os.mkdir(RELEASE_DIR)
        os.chdir(RELEASE_DIR)

    go_build()