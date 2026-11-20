# dmgbuild settings for the dschat drag-to-Applications installer.
import os

app = defines.get('app')
background = defines.get('background')
volume_name = defines.get('volume_name', 'dschat')

files = [app]
symlinks = {'Applications': '/Applications'}
icon_locations = {
    os.path.basename(app): (160, 235),
    'Applications': (480, 235),
}
background = background
window_rect = ((120, 120), (640, 400))
default_view = 'icon-view'
icon_size = 96
text_size = 14
arrangement = 'none'
show_status_bar = False
show_tab_view = False
show_pathbar = False
show_sidebar = False
format = 'UDZO'
compression_level = 9
