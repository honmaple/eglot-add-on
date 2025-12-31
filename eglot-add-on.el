;;; eglot-add-on.el --- The add-on-mode for Eglot  -*- lexical-binding: t; -*-

;; Copyright (C) 2025 honmaple

;; Author: lin.jiang <mail@honmaple.com>
;; URL: https://github.com/honmaple/eglot-add-on

;; This file is free software: you can redistribute it and/or modify
;; it under the terms of the GNU General Public License as published by
;; the Free Software Foundation, either version 3 of the License, or
;; (at your option) any later version.

;; This file is distributed in the hope that it will be useful,
;; but WITHOUT ANY WARRANTY; without even the implied warranty of
;; MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
;; GNU General Public License for more details.

;; You should have received a copy of the GNU General Public License
;; along with this file.  If not, see <http://www.gnu.org/licenses/>.

;;; Commentary:
;;
;; eglot add on.
;;

;;; Code:

(require 'eglot)

(defgroup eglot-add-on nil
  "Eglot support multi server."
  :group 'eglot)

(defcustom eglot-add-on-modes
  '((web-mode
     :add-on (web-emmet-mode)
     :completion (web-mode web-emmet-mode)
     :diagnostics web-mode))
  "The modes that would start multi server."
  :type '(list)
  :group 'eglot-add-on)

(defvar eglot-add-on-major-mode nil)
(defvar-local eglot-add-on--cached-server (make-hash-table :test #'equal)
  "A cached reference to the current Eglot server.")

(defun eglot-add-on--modes(modes keyword)
  (let* ((item (cl-loop for mode in (eglot--ensure-list modes)
                        thereis
                        (cdr (assoc mode eglot-add-on-modes))))
         (result (plist-get item keyword)))
    (when result (eglot--ensure-list result))))

(defun eglot-add-on--server-around (oldfunc &rest args)
  (let ((server eglot--cached-server)
        (eglot--cached-server (gethash major-mode eglot-add-on--cached-server)))
    (prog1 (apply oldfunc args)
      (puthash major-mode eglot--cached-server eglot-add-on--cached-server)
      (when eglot-add-on-major-mode
        (setq eglot--cached-server server)))))

(defun eglot-add-on--connect-around (oldfunc &rest args)
  (prog1 (apply oldfunc args)
    (let ((add-on-modes (eglot-add-on--modes (car args) :add-on)))
      (cl-loop for mode in add-on-modes
               unless (eq major-mode mode)
               do (when (eglot--lookup-mode mode)
                    (let* ((eglot-add-on-major-mode major-mode)
                           (major-mode mode))
                      (apply oldfunc (eglot--guess-contact))))))))

(defun eglot-add-on--shutdown-around (oldfunc &rest args)
  (prog1 (apply oldfunc args)
    (let ((add-on-modes (eglot-add-on--modes major-mode :add-on)))
      (cl-loop for mode in add-on-modes
               unless (eq major-mode mode)
               do (let* ((eglot-add-on-major-mode major-mode)
                         (major-mode mode))
                    (apply oldfunc args))))))

(defun eglot-add-on--completion-at-point-around (oldfunc &rest args)
  (let ((completion-modes (eglot-add-on--modes major-mode :completion)))
    (apply 'cape-wrap-super
           (cl-loop for mode in completion-modes
                    if (eq major-mode mode)
                    collect (lambda() (apply oldfunc args))
                    else
                    collect (lambda()
                              (let* ((eglot-add-on-major-mode major-mode)
                                     (major-mode mode))
                                (apply oldfunc args)))))))

;; (defun eglot--report-to-flymake (diags)
;;   "Internal helper for `eglot-flymake-backend'."
;;   (save-restriction
;;     (widen)
;;     (funcall eglot--current-flymake-report-fn diags
;;              ;; If the buffer hasn't changed since last
;;              ;; call to the report function, flymake won't
;;              ;; delete old diagnostics.  Using :region
;;              ;; keyword forces flymake to delete
;;              ;; them (github#159).
;;              :region (cons (point-min) (point-max))))
;;   (setq eglot--diagnostics diags))

;;;###autoload
(define-minor-mode eglot-add-on-mode
  "Mode for eglot add multi server support."
  :global t :group 'eglot
  (cond
   (eglot-add-on-mode
    (advice-add 'eglot--connect :around #'eglot-add-on--connect-around)
    (advice-add 'eglot-shutdown :around #'eglot-add-on--shutdown-around)
    (advice-add 'eglot-current-server :around #'eglot-add-on--server-around)
    (advice-add 'eglot-completion-at-point :around #'eglot-add-on--completion-at-point-around))
   (t
    (advice-remove 'eglot--connect #'eglot-add-on--connect-around)
    (advice-remove 'eglot-shutdown #'eglot-add-on--shutdown-around)
    (advice-remove 'eglot-current-server #'eglot-add-on--server-around)
    (advice-remove 'eglot-completion-at-point #'eglot-add-on--completion-at-point-around))))

;;; eglot-add-on.el ends here