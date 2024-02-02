import setuptools

with open("README.md", "r", encoding = "utf-8") as fh:
    long_description = fh.read()

install_requires = []
with open("requirements.txt") as f:
    for line in f.readlines():
        req = line.strip()
        if not req or req.startswith(("-e", "#")):
            continue
        install_requires.append(req)

setuptools.setup(
    name = "interlink",
    version = "0.0.1",
    author = "Diego Ciangottini",
    author_email = "diego.ciangottini@gmail.com",
    description = "interlink provider library",
    long_description = long_description,
    long_description_content_type = "text/markdown",
    url = "package URL",
    project_urls = {
    },
    classifiers = [
        "Programming Language :: Python :: 3",
        "Operating System :: OS Independent",
    ],
    packages = ["interlink"],
    python_requires = ">=3.6",
    install_requires = install_requires 
)


