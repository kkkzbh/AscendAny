import { Toaster } from 'sonner'
import { useTheme } from '../theme/ThemeProvider'

export default function GlobalToaster() {
  const { resolved } = useTheme()

  return (
    <Toaster
      theme={resolved}
      position="top-right"
      richColors
      closeButton
      toastOptions={{
        classNames: {
          toast: 'asc-toast',
          title: 'asc-toast-title',
          description: 'asc-toast-desc',
          actionButton: 'asc-toast-action',
          cancelButton: 'asc-toast-cancel',
          closeButton: 'asc-toast-close',
        },
      }}
    />
  )
}
