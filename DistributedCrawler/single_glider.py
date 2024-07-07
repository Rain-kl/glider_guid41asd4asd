import subprocess
import os
import threading
import time
import sys
import shutil
from pydantic import BaseModel

CORE_APP = "glider"


class GliderConfig(BaseModel):
    verbose: bool = False
    listen: int = 50010
    strategy: str = 'lha'
    check: str = "http://www.msftconnecttest.com/connecttest.txt#expect=200"
    checkinterval: int = 60


def start_process():
    p = subprocess.Popen(fr"core\tmp\{CORE_APP}.exe --config core\tmp\glider.conf", shell=True,
                         stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    while True:
        line = p.stdout.readline()
        if not line:
            break
        print(line.decode('gbk'))
    p.wait()


def generate_config(base_config: GliderConfig) -> str:
    # 生成配置文件
    config = (
        f"listen=:{base_config.listen}\n"
        f"verbose={base_config.verbose}\n"
        f"strategy={base_config.strategy}\n"
        f"check={base_config.check}\n"
        f"checkinterval={base_config.checkinterval}\n"
    )
    with open('./core/glider.conf', 'r', encoding='utf8') as f:
        config += f.read()
    return config


def run_glider(glider_config: GliderConfig):
    # 启动glider
    if not os.path.exists("./core/tmp"):
        os.makedirs("./core/tmp")
    process = []

    shutil.copyfile(f"./core/{CORE_APP}.exe", f"./core/tmp/{CORE_APP}.exe")
    config = generate_config(glider_config)
    with open(f"./core/tmp/glider.conf", "w", encoding='utf8') as f:
        f.write(config)
    p = threading.Thread(target=start_process)
    p.start()
    process.append(p)
    for p in process:
        p.join()
    print("All glider process are finished.")


if __name__ == "__main__":
    run_glider(
        GliderConfig(
            verbose=True,
            listen=8888,
            strategy="rr",
            check="http://www.msftconnecttest.com/connecttest.txt#expect=200",
            checkinterval=60
        )
    )
