import os

def find_cs_files_and_write_to_txt(directory, output_file):
    # 打开 output.txt 文件以写入
    with open(output_file, 'w', encoding='utf-8') as output:
        # 遍历当前目录及所有子目录
        for root, dirs, files in os.walk(directory):
            for file in files:
                if file.endswith('.go'):
                    cs_file_path = os.path.join(root, file)
                    print(f"正在处理文件: {cs_file_path}")
                    try:
                        # 读取 .cs 文件内容并写入到 output.txt
                        with open(cs_file_path, 'r', encoding='utf-8') as cs_file:
                            content = cs_file.read()
                            output.write(f"// 内容来自: {cs_file_path}\n")
                            output.write(content)
                            output.write("\n\n" + "="*40 + "\n\n")  # 添加分隔线
                    except Exception as e:
                        print(f"读取文件 {cs_file_path} 时出错: {e}")
    print(f"所有 .cs 文件的内容已经写入到 {output_file}")

if __name__ == "__main__":
    # 设置目标路径为你指定的路径
    directory = r"E:\unity project\unityserverupgrade"  # 这里直接指定目标目录
    output_file = '20260215server.txt'  # 输出文件名称
    find_cs_files_and_write_to_txt(directory, output_file)
