sys.path.extend([
    os.path.join(pkgdir, 'win32'),
    os.path.join(pkgdir, 'win32', 'lib'),
    os.path.join(pkgdir, 'Pythonwin'),
])

# Preload pywintypes and pythoncom
pwt = os.path.join(pkgdir, 'pypiwin32_system32', 'pywintypes36.dll')
pcom = os.path.join(pkgdir, 'pypiwin32_system32', 'pythoncom36.dll')
import imp
imp.load_dynamic('pywintypes', pwt)
imp.load_dynamic('pythoncom', pcom)

