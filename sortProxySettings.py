import json

file_path = 'proxySettings.json'

with open(file_path, 'r') as file:
    data = json.load(file)

sorted_extMimeTypes = dict(sorted(data['extMimeTypes'].items()))
data['extMimeTypes'] = sorted_extMimeTypes

# Save the sorted data back to the file
with open(file_path, 'w') as file:
    json.dump(data, file, indent=2)