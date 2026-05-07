from lxml import etree

root = etree.Element("greeting")
root.text = "hello from python"
print(etree.tostring(root, encoding="unicode"))
