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
    listen: str = ""
    strategy: str = 'lha'
    check: str = "http://www.msftconnecttest.com/connecttest.txt#expect=200"
    checkinterval: int = 60


def run_glider(port: int):
    p = subprocess.Popen(fr"core\tmp\{CORE_APP}_{port}.exe --config core\tmp\glider_{port}.conf", shell=True,
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
        content = str(f.read())
    list_content = content.split('\n')
    # for line in list_content:
    print(list_content)
    return config


def multi_start_glider(port_range: range):
    # 启动glider
    if not os.path.exists("./core/tmp"):
        os.makedirs("./core/tmp")
    process = []
    for port in port_range:
        shutil.copyfile(f"./core/{CORE_APP}.exe", f"./core/tmp/{CORE_APP}_{port}.exe")
        config = generate_config(
            GliderConfig(
                verbose=True,
                listen=f":{port}",
                strategy="lha",
                check="http://www.msftconnecttest.com/connecttest.txt#expect=200",
                checkinterval=60
            ))
        with open(f"./core/tmp/glider_{port}.conf", "w", encoding='utf8') as f:
            f.write(config)
        p = threading.Thread(target=run_glider, args=(port,))
        p.start()
        process.append(p)
    for p in process:
        p.join()
    print("All glider process are finished.")
    # shutil.rmtree("./core/tmp")
    # print("All temp files are removed.")
    # sys.exit(0)


if __name__ == "__main__":
    rsp = generate_config(
        GliderConfig(
            verbose=True,
            listen=f":8888",
            strategy="lha",
            check="http://www.msftconnecttest.com/connecttest.txt#expect=200",
            checkinterval=60
        ))
    print(rsp)
    # multi_start_glider(range(50000, 50003))
